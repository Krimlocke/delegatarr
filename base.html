import io
import json
import os
import threading
import logging
import pytz

from flask import Blueprint, render_template, request, redirect, url_for, send_file, send_from_directory, flash, current_app, abort

# Internal module dependencies
from delegatarr.config import (
    load_json, save_json, get_settings, apply_timezone,
    GROUPS_FILE, RULES_FILE, SETTINGS_FILE, APP_VERSION, LOG_FILE, LOGO_FILE
)
from delegatarr.deluge import get_deluge_status
from delegatarr.engine import get_dashboard_data, process_torrents, config_lock

# Initialize the Blueprint for route registration
bp = Blueprint('main', __name__)
logger = logging.getLogger(__name__)

SAFE_RETURN_URLS = {'/trackers', '/rules', '/logs', '/settings'}

def render_page(template_name, **kwargs):
    """Injects standard application state variables into the specified template."""
    kwargs.setdefault('version', APP_VERSION)
    kwargs['deluge_connected'] = get_deluge_status()
    kwargs.setdefault('app_settings', get_settings())
    kwargs['api_token'] = current_app.config.get('API_TOKEN', '')
    return render_template(template_name, **kwargs)

def get_tail_logs(lines_count=1500):
    """Helper to fetch and reverse the end of the log file, preventing code duplication."""
    if os.path.exists(LOG_FILE):
        try:
            with open(LOG_FILE, 'r') as f:
                lines = f.readlines()[-lines_count:]
                lines.reverse()
                if lines:
                    return "".join(lines)
        except OSError as e:
            logger.error(f"Failed to read log file: {e}")
    return "No logs generated yet."

def parse_rule_form(form):
    """Helper to cast and validate rule form data to prevent logic duplication."""
    try:
        threshold_value = float(form.get('threshold_value', 0))
    except (ValueError, TypeError):
        threshold_value = 0.0

    try:
        min_torrents = int(form.get('min_torrents', 0))
    except (ValueError, TypeError):
        min_torrents = 0

    raw_ratio = form.get('seed_ratio')
    try:
        seed_ratio = float(raw_ratio) if raw_ratio and raw_ratio.strip() != "" else None
    except ValueError:
        seed_ratio = None

    sort_order = form.get('sort_order', 'oldest_added')
    if sort_order == 'oldest_first': sort_order = 'oldest_added'
    if sort_order == 'newest_first': sort_order = 'newest_added'

    return {
        'group_id': form.get('group_id', '').strip()[:100],
        'label': form.get('label', '').strip()[:100],
        'target_state': form.get('target_state', 'All')[:50],
        'time_metric': form.get('time_metric', 'seeding_time')[:50],
        'min_torrents': min_torrents,
        'sort_order': sort_order,
        'threshold_value': threshold_value,
        'threshold_unit': form.get('threshold_unit', 'hours')[:20],
        'delete_data': 'delete_data' in form,
        'logic_operator': form.get('logic_operator', 'OR')[:10],
        'seed_ratio': seed_ratio
    }

def validate_rule(rule_data):
    """Validates a pre-parsed dictionary of rule parameters before saving."""
    if not rule_data.get('group_id'):
        return "Target Tag cannot be empty."
    if not rule_data.get('label'):
        return "Deluge Label cannot be empty."
    if rule_data.get('threshold_value', 0) <= 0:
        return "Threshold time must be greater than 0."
    if rule_data.get('min_torrents', 0) < 0:
        return "Min Keep cannot be negative."
    return None

@bp.route('/')
def index():
    return redirect(url_for('main.trackers'))

@bp.route('/trackers')
def trackers():
    summary, _ = get_dashboard_data()
    with config_lock:
        groups = load_json(GROUPS_FILE, {})
    return render_page('trackers.html', active_page='trackers', page_title='Tracker Configuration', tracker_summary=summary, groups=groups)

@bp.route('/rules')
def rules():
    _, unique_labels = get_dashboard_data()
    with config_lock:
        groups = load_json(GROUPS_FILE, {})
        rules_list = load_json(RULES_FILE, [])
    unique_tags = sorted(list(set(tag for tag in groups.values() if tag.strip())))
    return render_page('rules.html', active_page='rules', page_title='Removal Rules Engine', rules_list=rules_list, unique_tags=unique_tags, unique_labels=unique_labels)

@bp.route('/logs')
def view_logs():
    return render_page('logs.html', active_page='logs', page_title='Activity Logs', log_content=get_tail_logs())

@bp.route('/settings')
def settings_page():
    current_settings = get_settings()
    if 'timezone' not in current_settings:
        current_settings['timezone'] = os.environ.get('TZ', 'UTC')
    return render_page('settings.html', active_page='settings', page_title='Settings', app_settings=current_settings)

@bp.route('/save_settings', methods=['POST'])
def save_settings():
    try:
        new_interval = max(1, int(request.form.get('run_interval', 15)))
    except (ValueError, TypeError):
        new_interval = 15

    new_tz = request.form.get('timezone', 'UTC')
    if new_tz not in pytz.all_timezones:
        flash("Invalid timezone.", "error")
        return redirect(url_for('main.settings_page'))

    new_tracker_mode = request.form.get('tracker_mode', 'all')
    if new_tracker_mode not in ('all', 'top'):
        new_tracker_mode = 'all'

    new_dry_run = 'dry_run' in request.form

    settings_data = {
        'run_interval': new_interval,
        'timezone': new_tz,
        'tracker_mode': new_tracker_mode,
        'dry_run': new_dry_run
    }
    
    with config_lock:
        save_json(SETTINGS_FILE, settings_data)
        
    apply_timezone(new_tz)

    try:
        current_app.scheduler.reschedule_job('main_engine_job', trigger='interval', minutes=new_interval)
        dry_run_state = "ON" if new_dry_run else "OFF"
        logger.info(f"System: Settings updated. Interval: {new_interval}m, TZ: {new_tz}, Tracker Mode: {new_tracker_mode}, Dry Run: {dry_run_state}.")
        flash("Settings saved successfully.", "success")
    except Exception as e:
        logger.error(f"Failed to reschedule job: {e}")
        flash("Settings saved, but scheduler could not be updated. Restart may be required.", "warning")

    return redirect(url_for('main.settings_page'))

@bp.route('/export_settings')
def export_settings():
    with config_lock:
        backup_data = {
            'settings': load_json(SETTINGS_FILE, {}),
            'rules': load_json(RULES_FILE, []),
            'groups': load_json(GROUPS_FILE, {})
        }
    mem_file = io.BytesIO()
    mem_file.write(json.dumps(backup_data, indent=4).encode('utf-8'))
    mem_file.seek(0)
    return send_file(mem_file, as_attachment=True, download_name='delegatarr_backup.json', mimetype='application/json')

@bp.route('/import_settings', methods=['POST'])
def import_settings():
    if 'settings_file' not in request.files:
        flash("No file selected.", "error")
        return redirect(url_for('main.settings_page'))

    file = request.files['settings_file']
    if file.filename == '':
        flash("No file selected.", "error")
        return redirect(url_for('main.settings_page'))

    file_data = file.read()
    if len(file_data) > 5 * 1024 * 1024:
        flash("File too large. Upload limit is 5MB.", "error")
        return redirect(url_for('main.settings_page'))
    
    try:
        data = json.loads(file_data.decode('utf-8'))
    except (json.JSONDecodeError, UnicodeDecodeError) as e:
        logger.error(f"System Error: Failed to parse import file: {e}")
        flash("Import failed: file is not valid JSON.", "error")
        return redirect(url_for('main.settings_page'))

    # --- SECURITY VALIDATION ---
    if 'settings' in data and isinstance(data['settings'], dict):
        tz = data['settings'].get('timezone')
        if tz and tz not in pytz.all_timezones:
            flash("Import failed: Invalid timezone.", "error")
            return redirect(url_for('main.settings_page'))
        
        try:
            interval = int(data['settings'].get('run_interval', 15))
            if not (1 <= interval <= 1440):
                raise ValueError
        except (ValueError, TypeError):
            flash("Import failed: Invalid run interval.", "error")
            return redirect(url_for('main.settings_page'))

    if 'rules' in data and isinstance(data['rules'], list):
        for i, rule in enumerate(data['rules']):
            if not isinstance(rule, dict):
                flash(f"Import failed: Rule at index {i} is not a valid object.", "error")
                return redirect(url_for('main.settings_page'))

    if 'groups' in data and not isinstance(data['groups'], dict):
        flash("Import failed: Groups data is not a valid object.", "error")
        return redirect(url_for('main.settings_page'))
    # -------------------------------

    try:
        with config_lock:
            if 'settings' in data or 'rules' in data or 'groups' in data:
                if 'settings' in data and isinstance(data['settings'], dict): 
                    save_json(SETTINGS_FILE, data['settings'])
                if 'rules' in data and isinstance(data['rules'], list): 
                    save_json(RULES_FILE, data['rules'])
                if 'groups' in data and isinstance(data['groups'], dict): 
                    save_json(GROUPS_FILE, data['groups'])
            elif isinstance(data, dict):
                save_json(SETTINGS_FILE, data)
        logger.info("System: Full backup imported successfully.")
        
        try:
            imported_settings = load_json(SETTINGS_FILE, {})
            new_interval = int(imported_settings.get('run_interval', 15))
            current_app.scheduler.reschedule_job('main_engine_job', trigger='interval', minutes=new_interval)
            flash("Backup imported successfully.", "success")
        except Exception as sched_err:
            logger.error(f"Failed to reschedule job after import: {sched_err}")
            flash("Backup imported, but scheduler could not be updated. Restart may be required.", "warning")
        
    except Exception as e:
        logger.error(f"System Error: Failed to import backup file. {e}")
        flash(f"Import failed: {e}", "error")

    return redirect(url_for('main.settings_page'))

@bp.route('/factory_reset_settings', methods=['POST'])
def factory_reset_settings():
    default_settings = {
        'run_interval': 15,
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': True
    }
    with config_lock:
        save_json(SETTINGS_FILE, default_settings)
    
    try:
        current_app.scheduler.reschedule_job('main_engine_job', trigger='interval', minutes=15)
    except Exception as e:
        logger.error(f"Failed to reschedule job after factory reset: {e}")

    logger.info("System: Factory reset performed on application settings only.")
    flash("Settings reset to defaults. Rules and tags were not affected.", "warning")
    return redirect(url_for('main.settings_page'))

@bp.route('/factory_reset_all', methods=['POST'])
def factory_reset_all():
    default_settings = {
        'run_interval': 15,
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': True
    }
    with config_lock:
        save_json(SETTINGS_FILE, default_settings)
        save_json(RULES_FILE, [])
        save_json(GROUPS_FILE, {})
        
    try:
        current_app.scheduler.reschedule_job('main_engine_job', trigger='interval', minutes=15)
    except Exception as e:
        logger.error(f"Failed to reschedule job after full factory reset: {e}")

    logger.warning("System: CRITICAL - Full factory reset performed. All settings, rules, and tags have been wiped.")
    flash("Full factory reset complete. All settings, rules, and tags have been wiped.", "error")
    return redirect(url_for('main.settings_page'))

@bp.route('/update_groups', methods=['POST'])
def update_groups():
    with config_lock:
        groups = load_json(GROUPS_FILE, {})
        for tracker, group_id in request.form.items():
            if tracker == 'csrf_token': continue
            tracker = tracker.strip()[:255] # Security limit
            clean_val = group_id.strip()[:100] # Security limit
            if not clean_val:
                continue
            if clean_val.upper() == 'REMOVE':
                if tracker in groups:
                    del groups[tracker]
            else:
                groups[tracker] = clean_val
        save_json(GROUPS_FILE, groups)
        
    flash("Tracker tags saved.", "success")
    return redirect(url_for('main.trackers'))

@bp.route('/add_rule', methods=['POST'])
def add_rule():
    new_rule = parse_rule_form(request.form)
    error = validate_rule(new_rule)
    if error:
        flash(f"Rule not saved: {error}", "error")
        return redirect(url_for('main.rules'))

    with config_lock:
        rules_list = load_json(RULES_FILE, [])
        rules_list.append(new_rule)
        save_json(RULES_FILE, rules_list)
        
    flash("Rule added successfully.", "success")
    return redirect(url_for('main.rules'))

@bp.route('/edit_rule/<int:index>', methods=['POST'])
def edit_rule(index):
    updated_rule = parse_rule_form(request.form)
    error = validate_rule(updated_rule)
    if error:
        flash(f"Rule not updated: {error}", "error")
        return redirect(url_for('main.rules'))

    with config_lock:
        rules_list = load_json(RULES_FILE, [])
        if 0 <= index < len(rules_list):
            rules_list[index] = updated_rule
            save_json(RULES_FILE, rules_list)
            flash("Rule updated successfully.", "success")
        else:
            flash("Rule not found.", "error")

    return redirect(url_for('main.rules'))

@bp.route('/delete_rule/<int:index>', methods=['POST'])
def delete_rule(index):
    with config_lock:
        rules_list = load_json(RULES_FILE, [])
        if 0 <= index < len(rules_list):
            rules_list.pop(index)
            save_json(RULES_FILE, rules_list)
            flash("Rule deleted.", "warning")
            
    return redirect(url_for('main.rules'))

@bp.route('/run_now', methods=['POST'])
def run_now():
    threading.Thread(target=process_torrents, kwargs={'run_type': 'Manual'}, daemon=True).start()
    return_url = request.form.get('return_url', '/trackers')
    if return_url not in SAFE_RETURN_URLS:
        return_url = '/trackers'
    return redirect(return_url)

@bp.route('/api/logs')
def api_logs():
    # Token-based Auth: The secure way to handle automated JS requests
    if request.headers.get('X-API-Token') != current_app.config.get('API_TOKEN'):
        abort(403)
    return get_tail_logs()

@bp.route('/manifest.json')
def manifest():
    manifest_data = {
        "name": "Delegatarr",
        "short_name": "Delegatarr",
        "start_url": "/",
        "display": "standalone",
        "background_color": "#0f172a",
        "theme_color": "#0f172a",
        "icons": [
            {"src": url_for('main.favicon'), "sizes": "192x192", "type": "image/png"},
            {"src": url_for('main.favicon'), "sizes": "512x512", "type": "image/png"}
        ]
    }
    return current_app.response_class(response=json.dumps(manifest_data), status=200, mimetype='application/json')

@bp.route('/sw.js')
def service_worker():
    sw_js = """
    self.addEventListener('install', (e) => { self.skipWaiting(); });
    self.addEventListener('fetch', (e) => {});
    """
    return current_app.response_class(response=sw_js, status=200, mimetype='application/javascript')

@bp.route('/favicon.ico')
def favicon():
    if os.path.exists(LOGO_FILE):
        return send_from_directory(os.path.dirname(LOGO_FILE), os.path.basename(LOGO_FILE), mimetype='image/png')
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    if os.path.exists(os.path.join(base_dir, 'logo.png')):
        return send_from_directory(base_dir, 'logo.png', mimetype='image/png')
    return "", 404
