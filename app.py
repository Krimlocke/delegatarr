import io
import json
import logging
import os
import secrets
import ssl
import threading
import time
import urllib.request

from apscheduler.schedulers.background import BackgroundScheduler
from contextlib import contextmanager
from deluge_client import DelugeRPCClient
from flask import Flask, render_template, request, redirect, url_for, send_from_directory, send_file, flash
from flask_wtf.csrf import CSRFProtect
from logging.handlers import RotatingFileHandler
from waitress import serve

# --- VERSION CONTROL ---
APP_VERSION = "2026.04.06"

# --- INITIALIZE ENVIRONMENT ---
os.makedirs('/config', exist_ok=True)

app = Flask(__name__)

# --- GENERATE SECRET KEY ---
app.config['SECRET_KEY'] = secrets.token_hex(32) 
csrf = CSRFProtect(app)

# --- SETUP LOGGING ---
LOG_FILE = '/config/delegatarr.log'
log_formatter = logging.Formatter('[%(asctime)s] %(message)s', datefmt='%Y-%m-%d %H:%M:%S')

file_handler = RotatingFileHandler(LOG_FILE, maxBytes=10 * 1024 * 1024, backupCount=5)
file_handler.setFormatter(log_formatter)

console_handler = logging.StreamHandler()
console_handler.setFormatter(log_formatter)

app.logger.setLevel(logging.INFO)
app.logger.addHandler(file_handler)
app.logger.addHandler(console_handler)

# --- SECRET KEY MANAGEMENT ---
SECRET_KEY_FILE = '/config/secret.key'
if os.environ.get('SECRET_KEY'):
    app.secret_key = os.environ.get('SECRET_KEY')
else:
    try:
        with open(SECRET_KEY_FILE, 'rb') as f:
            app.secret_key = f.read()
    except FileNotFoundError:
        app.secret_key = os.urandom(24)
        with open(SECRET_KEY_FILE, 'wb') as f:
            f.write(app.secret_key)
        os.chmod(SECRET_KEY_FILE, 0o600)

scheduler = BackgroundScheduler()

# --- INFRASTRUCTURE CONFIGURATION ---
DELUGE_HOST = os.environ.get('DELUGE_HOST', '')
DELUGE_PORT = int(os.environ.get('DELUGE_PORT', 58846))
DELUGE_USER = os.environ.get('DELUGE_USER', '')
DELUGE_PASS = os.environ.get('DELUGE_PASS', '')
DELUGE_AUTH_FILE = os.environ.get('DELUGE_AUTH_FILE', '/config/deluge_auth')

# --- FILE PATHS ---
GROUPS_FILE = '/config/groups.json'
RULES_FILE = '/config/rules.json'
SETTINGS_FILE = '/config/settings.json'

# --- ENGINE CONCURRENCY & CACHING ---
_engine_lock = threading.Lock()
_cache_lock = threading.Lock()
_config_lock = threading.Lock()
SAFE_RETURN_URLS = {'/trackers', '/rules', '/logs', '/settings'}
_deluge_status_cache = {'status': False, 'timestamp': 0}

# --- HELPER FUNCTIONS ---
def get_deluge_credentials():
    user = DELUGE_USER
    password = DELUGE_PASS

    if DELUGE_AUTH_FILE and os.path.exists(DELUGE_AUTH_FILE):
        try:
            with open(DELUGE_AUTH_FILE, 'r') as f:
                for line in f:
                    line = line.strip()
                    if not line or line.startswith('#'):
                        continue
                    parts = line.split(':')
                    if len(parts) >= 2:
                        if user and parts[0] == user:
                            return parts[0], parts[1]
                        elif not user and (parts[0] == 'localclient' or (len(parts) >= 3 and parts[2] == '10')):
                            return parts[0], parts[1]
        except Exception as e:
            app.logger.error(f"System Error: Failed to parse auth file at {DELUGE_AUTH_FILE}: {e}")

    if not user:
        user = 'localclient'
    return user, password

def get_deluge_client():
    user, password = get_deluge_credentials()
    client = DelugeRPCClient(DELUGE_HOST, DELUGE_PORT, user, password)
    
    try:
        client.connect()
        return client
    except ssl.SSLEOFError:
        app.logger.warning("Deluge Error: Daemon is currently restarting and not ready for secure connections. Retrying later.")
        return None
    except Exception as e:
        app.logger.error(f"Deluge Error: {str(e)}")
        return None

@contextmanager
def deluge_session():
    client = None
    try:
        client = get_deluge_client()
        if client is None:
            raise ConnectionError("Could not establish Deluge connection.")
        yield client
    finally:
        if client and client.connected:
            client.disconnect()

def wait_for_deluge(max_retries=12, delay_seconds=5):
    if not DELUGE_HOST:
        app.logger.info("System: DELUGE_HOST not configured, skipping connection wait.")
        return False

    app.logger.info("System: Waiting for Deluge daemon to become available...")
    for attempt in range(1, max_retries + 1):
        try:
            with deluge_session() as client:
                client.call('daemon.info')
            app.logger.info(f"System: Deluge connection established. (Attempt {attempt}/{max_retries})")
            return True
        except Exception as e:
            err_str = str(e).lower()
            if "auth" in err_str or "login" in err_str or "password" in err_str:
                app.logger.error(f"System Error: Deluge authentication failed. Please check credentials. ({e})")
                break
            app.logger.warning(f"System: Deluge not ready yet (Attempt {attempt}/{max_retries}). Retrying in {delay_seconds}s...")
            time.sleep(delay_seconds)

    app.logger.warning("System Warning: Deluge did not respond in time or authentication failed. Proceeding with startup, but scheduled runs may fail.")
    return False

def load_json(filepath, default_val):
    if os.path.exists(filepath):
        try:
            with open(filepath, 'r') as f:
                return json.load(f)
        except json.JSONDecodeError:
            app.logger.warning(f"System Warning: {filepath} is corrupted or empty. Loading defaults.")
            return default_val
    return default_val

def save_json(filepath, data):
    tmp = filepath + '.tmp'
    with open(tmp, 'w') as f:
        json.dump(data, f, indent=4)
    os.replace(tmp, filepath)

def get_settings():
    return load_json(SETTINGS_FILE, {
        'run_interval': 15,
        'log_retention_days': 30,
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': True
    })

def apply_timezone(tz_string):
    os.environ['TZ'] = tz_string
    if hasattr(time, 'tzset'):
        time.tzset()

def download_default_logo():
    logo_path = '/config/logo.png'
    logo_url = 'https://raw.githubusercontent.com/Krimlocke/delegatarr/refs/heads/main/logo.png'
    if not os.path.exists(logo_path):
        try:
            app.logger.info("System: Logo missing. Downloading default from GitHub...")
            ctx = ssl.create_default_context()
            req = urllib.request.Request(logo_url, headers={'User-Agent': 'Mozilla/5.0'})
            with urllib.request.urlopen(req, timeout=5, context=ctx) as response, open(logo_path, 'wb') as out_file:
                out_file.write(response.read())
            app.logger.info("System: Default logo downloaded successfully.")
        except Exception as e:
            app.logger.error(f"System Error: Failed to download default logo: {e}")

def get_deluge_status():
    if not DELUGE_HOST:
        return False

    global _deluge_status_cache
    
    with _cache_lock:
        if time.time() - _deluge_status_cache['timestamp'] < 10:
            return _deluge_status_cache['status']
        _deluge_status_cache['timestamp'] = time.time()
            
    try:
        with deluge_session() as client:
            client.call('daemon.info')
        with _cache_lock:
            _deluge_status_cache = {'status': True, 'timestamp': time.time()}
        return True
    except Exception:
        with _cache_lock:
            _deluge_status_cache = {'status': False, 'timestamp': time.time()}
        return False

def get_dashboard_data():
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
        app.logger.error(f"Deluge Error: {e}")
        return {}, []

def validate_rule(form):
    group_id = form.get('group_id', '').strip()
    label = form.get('label', '').strip()
    try:
        threshold_val = float(form.get('threshold_value', form.get('max_hours', 0)))
    except (ValueError, TypeError):
        return "Threshold time must be a valid number."
    try:
        min_torrents = int(form.get('min_torrents', 0))
    except (ValueError, TypeError):
        return "Min Keep must be a valid integer."
        
    raw_ratio = form.get('seed_ratio')
    if raw_ratio and raw_ratio.strip():
        try:
            ratio_val = float(raw_ratio)
            if ratio_val < 0: return "Seed ratio cannot be negative."
        except ValueError:
            return "Seed ratio must be a valid number."

    if not group_id:
        return "Target Tag cannot be empty."
    if not label:
        return "Deluge Label cannot be empty."
    if threshold_val <= 0:
        return "Threshold time must be greater than 0."
    if min_torrents < 0:
        return "Min Keep cannot be negative."
    return None

def process_torrents(run_type="Scheduled"):
    if not _engine_lock.acquire(blocking=False):
        app.logger.info(f"{run_type} Engine Run: Skipped. Another run is already in progress.")
        return

    try:
        with _config_lock:
            groups = load_json(GROUPS_FILE, {})
            rules_list = load_json(RULES_FILE, [])

        if not rules_list or not groups:
            app.logger.info(f"{run_type} Engine Run: Skipped. No tags or rules are configured yet.")
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

                if sort_order == 'oldest_added':
                    # Protects newest at the top, pushes oldest to the bottom for removal
                    matching_torrents.sort(key=lambda x: x['time_added'], reverse=True) 
                elif sort_order == 'newest_added':
                    # Protects oldest at the top, pushes newest to the bottom for removal
                    matching_torrents.sort(key=lambda x: x['time_added'], reverse=False) 
                elif sort_order == 'longest_seeding':
                    # Protects shortest seeding at the top, pushes longest seeding to the bottom for removal
                    matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=False) 
                elif sort_order == 'shortest_seeding':
                    # Protects longest seeding at the top, pushes shortest seeding to the bottom for removal
                    matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=True)

                candidates_for_removal = matching_torrents[min_torrents:] if min_torrents > 0 else matching_torrents

                for t in candidates_for_removal:
                    if t['id'] in seen_ids:
                        app.logger.debug(f"Skipping '{t['name']}': already queued for removal by an earlier rule.")
                        continue
                        
                    time_condition_met = t['trigger_value'] >= rule_max_hours
                    
                    rule_ratio = rule.get('seed_ratio')
                    if rule_ratio is not None:
                        ratio_condition_met = t['ratio'] >= rule_ratio
                        if rule.get('logic_operator') == 'AND':
                            meets_removal_criteria = time_condition_met and ratio_condition_met
                        else:
                            meets_removal_criteria = time_condition_met or ratio_condition_met
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
                        app.logger.info(f"[DRY RUN] Would have removed: '{t['name']}' (Tag: {t['tag']}, State: {t['state']}, Metric: {t['metric']}, Delete Data: {t['delete_data']})")
                        removed_count += 1
                else:
                    for t in torrents_to_remove:
                        try:
                            client.call('core.remove_torrent', t['id'], t['delete_data'])
                            app.logger.info(f"Rule Matched! Removed: '{t['name']}' (Tag: {t['tag']}, State: {t['state']}, Metric: {t['metric']}, Delete Data: {t['delete_data']})")
                            removed_count += 1
                        except Exception as del_err:
                            app.logger.error(f"Failed to remove '{t['name']}': {del_err}")

        if not torrents_to_remove:
            app.logger.info(f"{run_type} Engine Run: Checked Deluge, no torrents met removal criteria.")
        else:
            mode_text = "[DRY RUN] " if is_dry_run else ""
            action_text = "identified" if is_dry_run else "removed"
            app.logger.info(f"{mode_text}{run_type} Engine Run: Completed. Successfully {action_text} {removed_count} torrent(s).")

    except ConnectionResetError:
        app.logger.error("Engine Run: Skipped. Deluge actively refused the connection (Deluge possibly restarting).")
    except (ssl.SSLEOFError, ssl.SSLError, EOFError, ConnectionRefusedError):
        app.logger.error("Engine Run: Skipped. Lost connection to Deluge (Daemon likely offline).")
    except Exception as e:
        app.logger.error(f"Background Task Error: {e}")
    finally:
        _engine_lock.release()

# --- ROUTES ---
def render_page(**kwargs):
    kwargs.setdefault('version', APP_VERSION)
    kwargs['deluge_connected'] = get_deluge_status()
    kwargs.setdefault('app_settings', get_settings())
    return render_template('index.html', **kwargs)

@app.route('/')
def index():
    return redirect(url_for('trackers'))

@app.route('/trackers')
def trackers():
    summary, _ = get_dashboard_data()
    with _config_lock:
        groups = load_json(GROUPS_FILE, {})
    return render_page(active_page='trackers', page_title='Tracker Configuration', tracker_summary=summary, groups=groups)

@app.route('/rules')
def rules():
    _, unique_labels = get_dashboard_data()
    with _config_lock:
        groups = load_json(GROUPS_FILE, {})
        rules_list = load_json(RULES_FILE, [])
    unique_tags = sorted(list(set(tag for tag in groups.values() if tag.strip())))
    return render_page(active_page='rules', page_title='Removal Rules Engine', rules_list=rules_list, unique_tags=unique_tags, unique_labels=unique_labels)

@app.route('/logs')
def view_logs():
    log_content = "No logs generated yet."
    if os.path.exists(LOG_FILE):
        with open(LOG_FILE, 'r') as f:
            lines = f.readlines()[-1500:]
            lines.reverse()
            if lines:
                log_content = "".join(lines)
    return render_page(active_page='logs', page_title='Activity Logs', log_content=log_content)

@app.route('/settings')
def settings_page():
    current_settings = get_settings()
    if 'timezone' not in current_settings:
        current_settings['timezone'] = os.environ.get('TZ', 'UTC')
    return render_page(active_page='settings', page_title='Settings', app_settings=current_settings)

@app.route('/save_settings', methods=['POST'])
def save_settings():
    try:
        new_interval = max(1, int(request.form.get('run_interval', 15)))
    except (ValueError, TypeError):
        new_interval = 15

    new_tz = request.form.get('timezone', 'UTC')
    new_tracker_mode = request.form.get('tracker_mode', 'all')
    new_dry_run = 'dry_run' in request.form

    settings_data = {
        'run_interval': new_interval,
        'timezone': new_tz,
        'tracker_mode': new_tracker_mode,
        'dry_run': new_dry_run
    }
    
    with _config_lock:
        save_json(SETTINGS_FILE, settings_data)
        
    apply_timezone(new_tz)

    try:
        scheduler.reschedule_job('main_engine_job', trigger='interval', minutes=new_interval)
        dry_run_state = "ON" if new_dry_run else "OFF"
        app.logger.info(f"System: Settings updated. Interval: {new_interval}m, TZ: {new_tz}, Tracker Mode: {new_tracker_mode}, Dry Run: {dry_run_state}.")
        flash("Settings saved successfully.", "success")
    except Exception as e:
        app.logger.error(f"Failed to reschedule job: {e}")
        flash("Settings saved, but scheduler could not be updated. Restart may be required.", "warning")

    return redirect(url_for('settings_page'))

@app.route('/export_settings')
def export_settings():
    with _config_lock:
        backup_data = {
            'settings': load_json(SETTINGS_FILE, {}),
            'rules': load_json(RULES_FILE, []),
            'groups': load_json(GROUPS_FILE, {})
        }
    mem_file = io.BytesIO()
    mem_file.write(json.dumps(backup_data, indent=4).encode('utf-8'))
    mem_file.seek(0)
    return send_file(mem_file, as_attachment=True, download_name='delegatarr_backup.json', mimetype='application/json')

@app.route('/import_settings', methods=['POST'])
def import_settings():
    if 'settings_file' in request.files:
        file = request.files['settings_file']
        if file.filename != '':
            file_data = file.read()
            if len(file_data) > 5 * 1024 * 1024:
                flash("File too large. Upload limit is 5MB.", "error")
                return redirect(url_for('settings_page'))
            
            try:
                data = json.loads(file_data.decode('utf-8'))
                with _config_lock:
                    if 'settings' in data or 'rules' in data or 'groups' in data:
                        if 'settings' in data and isinstance(data['settings'], dict): 
                            save_json(SETTINGS_FILE, data['settings'])
                        if 'rules' in data and isinstance(data['rules'], list): 
                            save_json(RULES_FILE, data['rules'])
                        if 'groups' in data and isinstance(data['groups'], dict): 
                            save_json(GROUPS_FILE, data['groups'])
                    elif isinstance(data, dict):
                        save_json(SETTINGS_FILE, data)
                app.logger.info("System: Full backup imported successfully.")
                flash("Backup imported successfully.", "success")
            except Exception as e:
                app.logger.error(f"System Error: Failed to import backup file. {e}")
                flash(f"Import failed: {e}", "error")
        else:
            flash("No file selected.", "error")
    return redirect(url_for('settings_page'))

@app.route('/factory_reset_settings', methods=['POST'])
def factory_reset_settings():
    default_settings = {
        'run_interval': 15,
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': True
    }
    with _config_lock:
        save_json(SETTINGS_FILE, default_settings)
    app.logger.info("System: Factory reset performed on application settings only.")
    flash("Settings reset to defaults. Rules and tags were not affected.", "warning")
    return redirect(url_for('settings_page'))

@app.route('/factory_reset_all', methods=['POST'])
def factory_reset_all():
    default_settings = {
        'run_interval': 15,
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': True
    }
    with _config_lock:
        save_json(SETTINGS_FILE, default_settings)
        save_json(RULES_FILE, [])
        save_json(GROUPS_FILE, {})
    app.logger.warning("System: CRITICAL - Full factory reset performed. All settings, rules, and tags have been wiped.")
    flash("Full factory reset complete. All settings, rules, and tags have been wiped.", "error")
    return redirect(url_for('settings_page'))

@app.route('/update_groups', methods=['POST'])
def update_groups():
    with _config_lock:
        groups = load_json(GROUPS_FILE, {})
        for tracker, group_id in request.form.items():
            clean_val = group_id.strip()
            if not clean_val:
                continue
            if clean_val.upper() == 'REMOVE':
                if tracker in groups:
                    del groups[tracker]
            else:
                groups[tracker] = clean_val
        save_json(GROUPS_FILE, groups)
        
    flash("Tracker tags saved.", "success")
    return redirect(url_for('trackers'))

@app.route('/add_rule', methods=['POST'])
def add_rule():
    error = validate_rule(request.form)
    if error:
        flash(f"Rule not saved: {error}", "error")
        return redirect(url_for('rules'))

    try:
        threshold_value = float(request.form.get('threshold_value', 0))
    except (ValueError, TypeError):
        threshold_value = 0.0

    try:
        min_torrents = int(request.form.get('min_torrents', 0))
    except (ValueError, TypeError):
        min_torrents = 0

    logic_operator = request.form.get('logic_operator', 'OR')
    raw_ratio = request.form.get('seed_ratio')

    try:
        seed_ratio = float(raw_ratio) if raw_ratio and raw_ratio.strip() != "" else None
    except ValueError:
        seed_ratio = None

    sort_order = request.form.get('sort_order', 'oldest_added')
    if sort_order == 'oldest_first': sort_order = 'oldest_added'
    if sort_order == 'newest_first': sort_order = 'newest_added'

    new_rule = {
        'group_id': request.form.get('group_id', '').strip(),
        'label': request.form.get('label', '').strip(),
        'target_state': request.form.get('target_state', 'All'),
        'time_metric': request.form.get('time_metric', 'seeding_time'),
        'min_torrents': min_torrents,
        'sort_order': sort_order,
        'threshold_value': threshold_value,
        'threshold_unit': request.form.get('threshold_unit', 'hours'),
        'delete_data': 'delete_data' in request.form,
        'logic_operator': logic_operator,
        'seed_ratio': seed_ratio
    }

    with _config_lock:
        rules_list = load_json(RULES_FILE, [])
        rules_list.append(new_rule)
        save_json(RULES_FILE, rules_list)
        
    flash("Rule added successfully.", "success")
    return redirect(url_for('rules'))

@app.route('/delete_rule/<int:index>', methods=['POST'])
def delete_rule(index):
    with _config_lock:
        rules_list = load_json(RULES_FILE, [])
        if 0 <= index < len(rules_list):
            rules_list.pop(index)
            save_json(RULES_FILE, rules_list)
            flash("Rule deleted.", "warning")
            
    return redirect(url_for('rules'))

@app.route('/run_now', methods=['POST'])
def run_now():
    threading.Thread(target=process_torrents, kwargs={'run_type': 'Manual'}, daemon=True).start()
    return_url = request.form.get('return_url', '/trackers')
    if return_url not in SAFE_RETURN_URLS:
        return_url = '/trackers'
    return redirect(return_url)

@app.route('/manifest.json')
def manifest():
    manifest_data = {
        "name": "Delegatarr",
        "short_name": "Delegatarr",
        "start_url": "/",
        "display": "standalone",
        "background_color": "#0f172a",
        "theme_color": "#0f172a",
        "icons": [
            {"src": url_for('favicon'), "sizes": "192x192", "type": "image/png"},
            {"src": url_for('favicon'), "sizes": "512x512", "type": "image/png"}
        ]
    }
    return app.response_class(response=json.dumps(manifest_data), status=200, mimetype='application/json')

@app.route('/sw.js')
def service_worker():
    sw_js = """
    self.addEventListener('install', (e) => { self.skipWaiting(); });
    self.addEventListener('fetch', (e) => {});
    """
    return app.response_class(response=sw_js, status=200, mimetype='application/javascript')

@app.route('/favicon.ico')
def favicon():
    if os.path.exists('/config/logo.png'):
        return send_from_directory('/config', 'logo.png', mimetype='image/png')
    base_dir = os.path.dirname(os.path.abspath(__file__))
    if os.path.exists(os.path.join(base_dir, 'logo.png')):
        return send_from_directory(base_dir, 'logo.png', mimetype='image/png')
    return "", 404

if __name__ == '__main__':
    threading.Thread(target=download_default_logo, daemon=True).start()

    boot_settings = get_settings()
    boot_interval = int(boot_settings.get('run_interval', 15))

    if 'timezone' in boot_settings:
        apply_timezone(boot_settings['timezone'])
    elif 'TZ' in os.environ:
        apply_timezone(os.environ['TZ'])

    wait_for_deluge()

    scheduler.add_job(func=process_torrents, trigger="interval", minutes=boot_interval, id='main_engine_job')
    scheduler.start()

    try:
        app.logger.info("System: Starting Waitress WSGI production server on port 5555...")
        serve(app, host='0.0.0.0', port=5555)
    except Exception as e:
        app.logger.error(f"System Error: Failed to start web server: {e}")
    finally:
        scheduler.shutdown()
