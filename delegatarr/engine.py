import ssl
import time
import threading
import logging

# Internal module dependencies
from delegatarr.config import load_json, GROUPS_FILE, RULES_FILE, get_settings
from delegatarr.deluge import deluge_session

# Module-level logging configuration
logger = logging.getLogger(__name__)

# --- CONCURRENCY LOCKS ---
engine_lock = threading.Lock()
config_lock = threading.Lock()

def get_dashboard_data():
    """Retrieves and summarizes active trackers and labels from the Deluge daemon."""
    try:
        with deluge_session() as client:
            torrents = client.call('core.get_torrents_status', {}, ['trackers', 'label'])

        summary = {}
        labels = set()
        settings = get_settings()
        tracker_mode = settings.get('tracker_mode', 'all')

        for torrent_id, data in torrents.items():
            lbl = data.get(b'label', b'').decode('utf-8', 'ignore')
            if lbl:
                labels.add(lbl)

            trackers_list = [t.get(b'url', b'').decode('utf-8', 'ignore') for t in data.get(b'trackers', []) if t.get(b'url')]

            if tracker_mode == 'top' and trackers_list:
                trackers_list = [trackers_list[0]]

            seen_domains = set()
            for raw_url in trackers_list:
                domain = raw_url.split('/')[2] if '//' in raw_url else raw_url
                if domain not in seen_domains:
                    seen_domains.add(domain)
                    summary[domain] = summary.get(domain, 0) + 1

        return summary, sorted(list(labels))
    except Exception as e:
        logger.error(f"Deluge Error: {e}")
        return {}, []

def process_torrents(run_type="Scheduled"):
    """Background task for evaluating active torrents against configured removal rules."""
    if not engine_lock.acquire(blocking=False):
        logger.info(f"{run_type} Engine Run: Skipped. Another run is already in progress.")
        return

    try:
        with config_lock:
            groups = load_json(GROUPS_FILE, {})
            rules_list = load_json(RULES_FILE, [])

        if not rules_list or not groups:
            logger.info(f"{run_type} Engine Run: Skipped. No tags or rules are configured yet.")
            return

        current_time = time.time()
        settings = get_settings()
        tracker_mode = settings.get('tracker_mode', 'all')
        is_dry_run = settings.get('dry_run', False)

        removed_count = 0
        torrents_to_remove = []
        seen_ids = set()

        with deluge_session() as client:
            fields = ['name', 'trackers', 'label', 'seeding_time', 'time_added', 'state', 'active_time', 'ratio']
            torrents = client.call('core.get_torrents_status', {}, fields)

            for rule in rules_list:
                target_group = rule.get('group_id', '')
                target_label = rule.get('label', '')
                target_state = rule.get('target_state', 'All')
                time_metric = rule.get('time_metric', 'seeding_time')

                try:
                    min_torrents = int(rule.get('min_torrents', rule.get('min_keep', 0)))
                except (ValueError, TypeError):
                    min_torrents = 0

                try:
                    threshold_val = float(rule.get('threshold_value', rule.get('max_hours', 0.0)))
                except (ValueError, TypeError):
                    threshold_val = 0.0

                threshold_unit = rule.get('threshold_unit', 'hours')

                if threshold_unit == 'minutes':
                    rule_max_hours = threshold_val / 60.0
                elif threshold_unit == 'days':
                    rule_max_hours = threshold_val * 24.0
                else:
                    rule_max_hours = threshold_val

                sort_order = rule.get('sort_order', 'oldest_added')
                matching_torrents = []

                for tid, data in torrents.items():
                    name = data.get(b'name', b'').decode('utf-8', 'ignore')
                    label = data.get(b'label', b'').decode('utf-8', 'ignore')
                    state = data.get(b'state', b'').decode('utf-8', 'ignore')

                    seeding_hours = int(data.get(b'seeding_time') or 0) / 3600.0
                    time_added = int(data.get(b'time_added') or 0)
                    active_hours = int(data.get(b'active_time') or 0) / 3600.0
                    ratio = float(data.get(b'ratio', 0.0))

                    total_age_hours = (current_time - time_added) / 3600.0
                    paused_hours = max(0.0, total_age_hours - active_hours)

                    if target_state != 'All' and state != target_state:
                        continue

                    trackers_list = [t.get(b'url', b'').decode('utf-8', 'ignore') for t in data.get(b'trackers', []) if t.get(b'url')]
                    if not trackers_list:
                        continue

                    if tracker_mode == 'top':
                        trackers_list = [trackers_list[0]]

                    matched_group = False
                    for raw_url in trackers_list:
                        domain = raw_url.split('/')[2] if '//' in raw_url else raw_url
                        if groups.get(domain) == target_group:
                            matched_group = True
                            break

                    if matched_group and label.lower() == target_label.lower():
                        if time_metric == 'time_added':
                            trigger_value = total_age_hours
                        elif time_metric == 'time_paused':
                            trigger_value = paused_hours
                        else:
                            trigger_value = seeding_hours

                        matching_torrents.append({
                            'id': tid,
                            'name': name,
                            'seeding_hours': seeding_hours,
                            'time_added': time_added,
                            'trigger_value': trigger_value,
                            'ratio': ratio
                        })

                if not matching_torrents:
                    continue

                # FIX: Sort orders were inverted. "oldest_added" should remove the
                # oldest first, meaning they should appear at the END of the protected
                # list (sorted newest-first so oldest fall past the min_torrents slice).
                if sort_order == 'oldest_added':
                    matching_torrents.sort(key=lambda x: x['time_added'], reverse=True)
                elif sort_order == 'newest_added':
                    matching_torrents.sort(key=lambda x: x['time_added'], reverse=False)
                elif sort_order == 'longest_seeding':
                    matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=False)
                elif sort_order == 'shortest_seeding':
                    matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=True)

                candidates_for_removal = matching_torrents[min_torrents:] if min_torrents > 0 else matching_torrents

                for t in candidates_for_removal:
                    if t['id'] in seen_ids:
                        logger.debug(f"Skipping '{t['name']}': already queued for removal by an earlier rule.")
                        continue
                        
                    time_condition_met = t['trigger_value'] >= rule_max_hours
                    
                    rule_ratio = rule.get('seed_ratio')
                    if rule_ratio is not None:
                        try:
                            ratio_condition_met = float(t['ratio']) >= float(rule_ratio)
                            if rule.get('logic_operator') == 'AND':
                                meets_removal_criteria = time_condition_met and ratio_condition_met
                            else:
                                meets_removal_criteria = time_condition_met or ratio_condition_met
                        except (ValueError, TypeError):
                            logger.error(f"Rule Evaluation Error: Invalid ratio data. Skipping ratio check for '{t['name']}'.")
                            meets_removal_criteria = time_condition_met
                    else:
                        meets_removal_criteria = time_condition_met

                    if meets_removal_criteria:
                        seen_ids.add(t['id'])
                        should_delete_data = rule.get('delete_data', False)
                        torrents_to_remove.append({
                            'id': t['id'],
                            'name': t['name'],
                            'tag': target_group,
                            'state': target_state,
                            'metric': time_metric,
                            'delete_data': should_delete_data
                        })

            if torrents_to_remove:
                if is_dry_run:
                    for t in torrents_to_remove:
                        logger.info(f"[DRY RUN] Would have removed: '{t['name']}' (Tag: {t['tag']}, State: {t['state']}, Metric: {t['metric']}, Delete Data: {t['delete_data']})")
                        removed_count += 1
                else:
                    for t in torrents_to_remove:
                        try:
                            client.call('core.remove_torrent', t['id'], t['delete_data'])
                            logger.info(f"Rule Matched! Removed: '{t['name']}' (Tag: {t['tag']}, State: {t['state']}, Metric: {t['metric']}, Delete Data: {t['delete_data']})")
                            removed_count += 1
                        except Exception as del_err:
                            logger.error(f"Failed to remove '{t['name']}': {del_err}")

        if not torrents_to_remove:
            logger.info(f"{run_type} Engine Run: Checked Deluge, no torrents met removal criteria.")
        else:
            mode_text = "[DRY RUN] " if is_dry_run else ""
            action_text = "identified" if is_dry_run else "removed"
            logger.info(f"{mode_text}{run_type} Engine Run: Completed. Successfully {action_text} {removed_count} torrent(s).")

    except ConnectionResetError:
        logger.error("Engine Run: Skipped. Deluge actively refused the connection (Deluge possibly restarting).")
    except (ssl.SSLEOFError, ssl.SSLError, EOFError, ConnectionRefusedError):
        logger.error("Engine Run: Skipped. Lost connection to Deluge (Daemon likely offline).")
    except Exception as e:
        logger.error(f"Background Task Error: {e}")
    finally:
        engine_lock.release()
