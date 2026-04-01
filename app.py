import os
import json
import time
import urllib.request
import secrets
import string
from functools import wraps
from datetime import datetime, timedelta
from flask import Flask, render_template_string, request, redirect, url_for, send_from_directory, send_file, Response
from deluge_client import DelugeRPCClient
from apscheduler.schedulers.background import BackgroundScheduler
from waitress import serve
import io

# --- INITIALIZE ENVIRONMENT ---
os.makedirs('/config', exist_ok=True)

app = Flask(__name__)
scheduler = BackgroundScheduler()

# --- VERSION CONTROL ---
APP_VERSION = "2026.04.02"

# --- SECURITY CONFIG ---
APP_USER = os.environ.get('APP_USER', 'admin')
APP_PASS = os.environ.get('APP_PASS')

# If the user didn't set a password in their Docker setup
if not APP_PASS:
    # Generate a secure 16-character random string
    alphabet = string.ascii_letters + string.digits
    APP_PASS = ''.join(secrets.choice(alphabet) for _ in range(16))
    
    print("\n" + "="*50)
    print(" ⚠️ SECURITY WARNING: No APP_PASS provided!")
    print(" A temporary password has been generated for this session.")
    print(f" Username: {APP_USER}")
    print(f" Password: {APP_PASS}")
    print(" Set the APP_PASS environment variable to disable this message.")
    print("="*50 + "\n")

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
LOG_FILE = '/config/delegatarr.log'

# --- SECURITY DECORATOR ---
def check_auth(username, password):
    return username == APP_USER and password == APP_PASS

def authenticate():
    return Response(
    'Could not verify your access level for that URL.\n'
    'You have to login with proper credentials', 401,
    {'WWW-Authenticate': 'Basic realm="Login Required"'})

def requires_auth(f):
    @wraps(f)
    def decorated(*args, **kwargs):
        auth = request.authorization
        if not auth or not check_auth(auth.username, auth.password):
            return authenticate()
        return f(*args, **kwargs)
    return decorated

# --- HELPER FUNCTIONS ---
def load_json(filepath, default_val):
    if os.path.exists(filepath):
        try:
            with open(filepath, 'r') as f:
                return json.load(f)
        except json.JSONDecodeError:
            write_log(f"System Warning: {filepath} is corrupted. Loading defaults.")
            return default_val
    return default_val

def save_json(filepath, data):
    with open(filepath, 'w') as f:
        json.dump(data, f, indent=4)

def get_settings():
    return load_json(SETTINGS_FILE, {
        'run_interval': 15, 
        'log_retention_days': 30, 
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': False  # New Safety feature
    })

def apply_timezone(tz_string):
    os.environ['TZ'] = tz_string
    if hasattr(time, 'tzset'):
        time.tzset()

def write_log(message):
    timestamp = datetime.now().strftime('%Y-%m-%d %H:%M:%S')
    log_entry = f"[{timestamp}] {message}"
    print(log_entry)
    try:
        with open(LOG_FILE, 'a') as f:
            f.write(log_entry + "\n")
    except Exception as e:
        print(f"Failed to write to log: {e}")

def cleanup_logs():
    if not os.path.exists(LOG_FILE):
        return
    write_log("Running automated log cleanup...")
    settings = get_settings()
    cutoff_date = datetime.now() - timedelta(days=int(settings.get('log_retention_days', 30)))
    valid_lines = []
    try:
        with open(LOG_FILE, 'r') as f:
            lines = f.readlines()
        for line in lines:
            try:
                time_str = line[1:20]
                log_date = datetime.strptime(time_str, '%Y-%m-%d %H:%M:%S')
                if log_date >= cutoff_date:
                    valid_lines.append(line)
            except ValueError:
                valid_lines.append(line)
        with open(LOG_FILE, 'w') as f:
            f.writelines(valid_lines)
    except Exception as e:
        print(f"Log cleanup error: {e}")

def download_default_logo():
    logo_path = '/config/logo.png'
    logo_url = 'https://raw.githubusercontent.com/Krimlocke/delegatarr/refs/heads/main/logo.png'
    if not os.path.exists(logo_path):
        try:
            write_log("System: Logo missing. Downloading default from GitHub...")
            req = urllib.request.Request(logo_url, headers={'User-Agent': 'Mozilla/5.0'})
            with urllib.request.urlopen(req, timeout=5) as response, open(logo_path, 'wb') as out_file:
                out_file.write(response.read())
            write_log("System: Default logo downloaded successfully.")
        except Exception as e:
            write_log(f"System Error: Failed to download default logo: {e}")

def get_deluge_credentials():
    user = DELUGE_USER
    password = DELUGE_PASS
    if DELUGE_AUTH_FILE and os.path.exists(DELUGE_AUTH_FILE):
        try:
            with open(DELUGE_AUTH_FILE, 'r') as f:
                for line in f:
                    line = line.strip()
                    if not line or line.startswith('#'): continue
                    parts = line.split(':')
                    if len(parts) >= 2:
                        if user and parts[0] == user: return parts[0], parts[1]
                        elif not user and (parts[0] == 'localclient' or (len(parts) >= 3 and parts[2] == '10')):
                            return parts[0], parts[1]
        except Exception as e:
            write_log(f"System Error: Failed to parse auth file: {e}")
    if not user: user = 'localclient'
    return user, password

def get_deluge_client():
    user, password = get_deluge_credentials()
    client = DelugeRPCClient(DELUGE_HOST, DELUGE_PORT, user, password)
    client.connect()
    return client

def get_dashboard_data():
    client = None
    try:
        client = get_deluge_client()
        torrents = client.call('core.get_torrents_status', {}, ['trackers', 'label'])
        summary = {}
        labels = set()
        settings = get_settings()
        tracker_mode = settings.get('tracker_mode', 'all')
        for torrent_id, data in torrents.items():
            lbl = data.get(b'label', b'').decode('utf-8', 'ignore')
            if lbl: labels.add(lbl)
            trackers_list = [t.get(b'url', b'').decode('utf-8', 'ignore') for t in data.get(b'trackers', []) if t.get(b'url')]
            if tracker_mode == 'top' and trackers_list: trackers_list = [trackers_list[0]]
            for raw_url in trackers_list:
                domain = raw_url.split('/')[2] if '//' in raw_url else raw_url
                summary[domain] = summary.get(domain, 0) + 1
        return summary, sorted(list(labels))
    except Exception as e:
        print(f"Deluge Error: {e}")
        return {}, []
    finally:
        if client and client.connected: client.disconnect()

def process_torrents(run_type="Scheduled"):
    groups = load_json(GROUPS_FILE, {})
    rules = load_json(RULES_FILE, [])
    settings = get_settings()
    is_dry_run = settings.get('dry_run', False)
    
    if not rules or not groups:
        write_log(f"{run_type} Engine Run: Skipped. No tags or rules configured.")
        return

    client = None
    try:
        client = get_deluge_client()
        fields = ['name', 'trackers', 'label', 'seeding_time', 'time_added', 'state']
        torrents = client.call('core.get_torrents_status', {}, fields)
        current_time = time.time()
        tracker_mode = settings.get('tracker_mode', 'all')
        removed_count = 0  
        
        for rule in rules:
            target_group = rule.get('group_id', '')
            target_label = rule.get('label', '')
            target_state = rule.get('target_state', 'All')
            time_metric = rule.get('time_metric', 'seeding_time')
            try: min_torrents = int(rule.get('min_torrents', 0))
            except: min_torrents = 0
            try: rule_max_hours = float(rule.get('max_hours', 0))
            except: rule_max_hours = 0.0

            sort_order = rule.get('sort_order', 'oldest_added')
            matching_torrents = []
            for tid, data in torrents.items():
                name = data.get(b'name', b'').decode('utf-8', 'ignore')
                label = data.get(b'label', b'').decode('utf-8', 'ignore')
                state = data.get(b'state', b'').decode('utf-8', 'ignore')
                seeding_hours = int(data.get(b'seeding_time') or 0) / 3600.0
                time_added = int(data.get(b'time_added') or 0)
                
                if target_state != 'All' and state != target_state: continue
                trackers_list = [t.get(b'url', b'').decode('utf-8', 'ignore') for t in data.get(b'trackers', []) if t.get(b'url')]
                if not trackers_list: continue
                if tracker_mode == 'top': trackers_list = [trackers_list[0]]
                
                matched_group = False
                for raw_url in trackers_list:
                    domain = raw_url.split('/')[2] if '//' in raw_url else raw_url
                    if groups.get(domain) == target_group:
                        matched_group = True
                        break
                
                if matched_group and label.lower() == target_label.lower():
                    trigger_value = (current_time - time_added) / 3600.0 if time_metric == 'time_added' else seeding_hours
                    matching_torrents.append({'id': tid, 'name': name, 'seeding_hours': seeding_hours, 'time_added': time_added, 'trigger_value': trigger_value})
            
            if not matching_torrents: continue
            if sort_order == 'oldest_added': matching_torrents.sort(key=lambda x: x['time_added'], reverse=True) 
            elif sort_order == 'newest_added': matching_torrents.sort(key=lambda x: x['time_added'], reverse=False)
            elif sort_order == 'longest_seeding': matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=False)
            elif sort_order == 'shortest_seeding': matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=True)
            
            candidates = matching_torrents[min_torrents:] if min_torrents > 0 else matching_torrents
            for t in candidates:
                if t['trigger_value'] >= rule_max_hours:
                    if is_dry_run:
                        write_log(f"[DRY RUN] Would remove: '{t['name']}' (Rule: {target_group})")
                        removed_count += 1
                    else:
                        try:
                            client.call('core.remove_torrent', t['id'], rule['delete_data'])
                            write_log(f"Removed: '{t['name']}' (Tag: {target_group})")
                            removed_count += 1
                        except Exception as del_err:
                            write_log(f"Failed to remove '{t['name']}': {del_err}")
        
        heartbeat = "Dry Run" if is_dry_run else run_type
        write_log(f"{heartbeat} Engine Run: {'No torrents met criteria' if removed_count == 0 else f'Processed {removed_count} torrent(s)'}")
                    
    except Exception as e:
        write_log(f"Engine Error: {e}")
    finally:
        if client and client.connected: client.disconnect()

# --- HTML TEMPLATE ---
MASTER_TEMPLATE = """
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{ page_title }} - Delegatarr</title>
    <link rel="manifest" href="{{ url_for('manifest') }}">
    <meta name="theme-color" content="#0f172a">
    <link rel="apple-touch-icon" href="{{ url_for('favicon') }}?v={{ version }}">
    <link rel="icon" type="image/png" href="{{ url_for('favicon') }}?v={{ version }}">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-main: #0f172a; --bg-sidebar: #1e293b; --bg-card: #1e293b; --bg-input: #0f172a;
            --text-main: #f8fafc; --text-muted: #94a3b8; --accent: #6366f1; --accent-hover: #4f46e5;
            --border-color: #334155; --danger: #ef4444; --danger-hover: #dc2626; --success: #10b981;
            --warning: #f59e0b;
        }
        * { box-sizing: border-box; }
        body { font-family: 'Inter', sans-serif; margin: 0; background-color: var(--bg-main); color: var(--text-main); display: flex; height: 100vh; overflow: hidden; }
        .sidebar { width: 260px; flex-shrink: 0; background-color: var(--bg-sidebar); display: flex; flex-direction: column; border-right: 1px solid var(--border-color); z-index: 1000; transition: width 0.25s ease; overflow: hidden; }
        .brand { display: flex; align-items: center; padding: 20px 15px; height: 78px; border-bottom: 1px solid var(--border-color); white-space: nowrap; }
        .brand-logo { width: 32px; height: 32px; border-radius: 4px; object-fit: contain; }
        .brand-text { font-size: 20px; font-weight: 700; margin-left: 12px; }
        .nav-menu { display: flex; flex-direction: column; padding: 20px 15px; gap: 8px; flex-grow: 1; }
        .nav-link { display: flex; align-items: center; padding: 12px 16px; color: var(--text-muted); text-decoration: none; border-radius: 8px; font-weight: 500; transition: all 0.2s ease; cursor: pointer; border: none; background: none; font-size: 15px; text-align: left; }
        .nav-link:hover { background-color: rgba(255, 255, 255, 0.05); color: var(--text-main); }
        .nav-link.active { background: linear-gradient(90deg, rgba(99,102,241,0.15) 0%, rgba(99,102,241,0) 100%); color: var(--accent); border-left: 3px solid var(--accent); }
        .nav-icon { width: 22px; height: 22px; margin-right: 12px; }
        .nav-action { background-color: var(--accent); color: white; font-weight: 600; justify-content: center; margin-top: 15px; }
        .main-content { flex-grow: 1; padding: 0 40px 40px 40px; overflow-y: auto; position: relative; }
        .top-header { display: flex; align-items: center; gap: 15px; padding: 25px 0; margin-bottom: 20px; z-index: 1050; }
        .page-header { font-size: 28px; font-weight: 700; margin: 0; }
        .dry-run-banner { background-color: var(--warning); color: #000; padding: 10px; text-align: center; font-weight: 700; font-size: 14px; position: sticky; top: 0; z-index: 2000; border-radius: 0 0 8px 8px; }
        .card { background-color: var(--bg-card); border-radius: 12px; border: 1px solid var(--border-color); padding: 24px; margin-bottom: 30px; }
        .table-wrapper { width: 100%; overflow-x: auto; border-radius: 6px; }
        table { width: 100%; border-collapse: collapse; font-size: 14px; min-width: 600px; }
        th { text-align: left; padding: 12px 16px; color: var(--text-muted); border-bottom: 2px solid var(--border-color); }
        td { padding: 16px; border-bottom: 1px solid var(--border-color); }
        input[type="text"], input[type="number"], select { padding: 10px 14px; border: 1px solid var(--border-color); border-radius: 6px; background: var(--bg-input); color: var(--text-main); }
        .btn { padding: 10px 16px; border: none; border-radius: 6px; cursor: pointer; font-weight: 600; text-decoration: none; display: inline-flex; align-items: center; font-size: 14px; white-space: nowrap;}
        .btn-primary { background-color: var(--accent); color: white; }
        .btn-danger { background-color: var(--danger); color: white; }
        .status-badge-yes { color: var(--danger); font-weight: bold; background: rgba(239, 68, 68, 0.1); padding: 4px 8px; border-radius: 4px; }
        .status-badge-no { color: var(--success); font-weight: bold; background: rgba(16, 185, 129, 0.1); padding: 4px 8px; border-radius: 4px; }
        @media (max-width: 768px) {
            .sidebar { position: fixed; left: -260px; height: 100%; }
            body.mobile-sidebar-open .sidebar { left: 0; }
            .main-content { padding: 0 15px; }
        }
    </style>
</head>
<body class="{{ 'sidebar-collapsed' if collapsed else '' }}">
    {% if dry_run_active %}
    <div class="dry-run-banner">SAFETY MODE ACTIVE: No torrents will be deleted. Check logs for simulation results.</div>
    {% endif %}
    
    <div class="sidebar" id="sidebar">
        <div class="brand">
            <img src="{{ url_for('favicon') }}" class="brand-logo">
            <span class="brand-text">Delegatarr</span>
        </div>
        <div class="nav-menu">
            <a href="{{ url_for('trackers') }}" class="nav-link {{ 'active' if active_page == 'trackers' }}">Tracker Config</a>
            <a href="{{ url_for('rules') }}" class="nav-link {{ 'active' if active_page == 'rules' }}">Removal Rules</a>
            <a href="{{ url_for('view_logs') }}" class="nav-link {{ 'active' if active_page == 'logs' }}">Activity Logs</a>
            <a href="{{ url_for('settings_page') }}" class="nav-link {{ 'active' if active_page == 'settings' }}">Settings</a>
            <div style="flex-grow: 1;"></div>
            <form action="{{ url_for('run_now') }}" method="POST">
                <button type="submit" class="nav-link nav-action">Run Engine Now</button>
            </form>
        </div>
        <div style="padding: 15px; font-size: 10px; color: var(--text-muted);">{{ version }}</div>
    </div>

    <div class="main-content">
        <div class="top-header"><h1 class="page-header">{{ page_title }}</h1></div>
        
        {% if active_page == 'trackers' %}
        <div class="card">
            <form action="{{ url_for('update_groups') }}" method="POST">
                <div class="table-wrapper">
                    <table>
                        <tr><th>Tracker Domain</th><th>Active Torrents</th><th>Tag Assignment</th></tr>
                        {% for tracker, count in tracker_summary.items() %}
                        <tr><td>{{ tracker }}</td><td>{{ count }}</td><td><input type="text" name="{{ tracker }}" value="{{ groups.get(tracker, '') }}" placeholder="Assign Tag..."></td></tr>
                        {% endfor %}
                    </table>
                </div>
                <button type="submit" class="btn btn-primary" style="margin-top:20px;">Save Tags</button>
            </form>
        </div>

        {% elif active_page == 'rules' %}
        <div class="card">
            <form action="{{ url_for('add_rule') }}" method="POST">
                <div style="display:flex; gap:10px; flex-wrap:wrap;">
                    <input name="group_id" placeholder="Target Tag" required>
                    <input name="label" placeholder="Deluge Label" required>
                    <select name="time_metric"><option value="seeding_time">Seeding Time ></option><option value="time_added">Time Since Added ></option></select>
                    <input type="number" name="max_hours" placeholder="Hours" step="any" required style="width:80px;">
                    <button type="submit" class="btn btn-primary">+ Add Rule</button>
                </div>
            </form>
            <div class="table-wrapper" style="margin-top:30px;">
                <table>
                    <tr><th>Tag</th><th>Label</th><th>Metric</th><th>Threshold</th><th>Del Data?</th><th>Action</th></tr>
                    {% for rule in rules %}
                    <tr>
                        <td>{{ rule.group_id }}</td><td>{{ rule.label }}</td><td>{{ rule.time_metric }}</td><td>{{ rule.max_hours }}h</td>
                        <td>{{ 'YES' if rule.delete_data else 'NO' }}</td>
                        <td><form action="{{ url_for('delete_rule', index=loop.index0) }}" method="POST"><button class="btn btn-danger">Delete</button></form></td>
                    </tr>
                    {% endfor %}
                </table>
            </div>
        </div>

        {% elif active_page == 'logs' %}
        <div class="card"><pre style="font-size:12px; color:var(--text-muted);">{{ log_content }}</pre></div>
        
        {% elif active_page == 'settings' %}
        <div class="card">
            <form action="{{ url_for('save_settings') }}" method="POST">
                <div style="margin-bottom:20px;">
                    <label style="display:flex; align-items:center; gap:10px; font-weight:bold; color:var(--warning);">
                        <input type="checkbox" name="dry_run" value="yes" {{ 'checked' if app_settings.get('dry_run') }}>
                        ENABLE GLOBAL DRY RUN (Safety Mode)
                    </label>
                    <p style="font-size:12px; color:var(--text-muted);">When enabled, no torrents will actually be deleted.</p>
                </div>
                <div>
                    <label>Engine Interval (Minutes)</label><br>
                    <input type="number" name="run_interval" value="{{ app_settings.get('run_interval', 15) }}">
                </div>
                <button type="submit" class="btn btn-primary" style="margin-top:20px;">Save Settings</button>
            </form>
        </div>
        {% endif %}
    </div>
</body>
</html>
"""

# --- ROUTES ---
@app.route('/')
@requires_auth
def index(): return redirect(url_for('trackers'))

@app.route('/trackers')
@requires_auth
def trackers():
    summary, _ = get_dashboard_data()
    groups = load_json(GROUPS_FILE, {})
    settings = get_settings()
    return render_template_string(MASTER_TEMPLATE, active_page='trackers', page_title='Trackers', tracker_summary=summary, groups=groups, version=APP_VERSION, dry_run_active=settings.get('dry_run'))

@app.route('/rules')
@requires_auth
def rules():
    groups = load_json(GROUPS_FILE, {})
    rules = load_json(RULES_FILE, [])
    settings = get_settings()
    return render_template_string(MASTER_TEMPLATE, active_page='rules', page_title='Rules', rules=rules, version=APP_VERSION, dry_run_active=settings.get('dry_run'))

@app.route('/logs')
@requires_auth
def view_logs():
    content = ""
    if os.path.exists(LOG_FILE):
        with open(LOG_FILE, 'r') as f: content = "".join(reversed(f.readlines()))
    settings = get_settings()
    return render_template_string(MASTER_TEMPLATE, active_page='logs', page_title='Logs', log_content=content, version=APP_VERSION, dry_run_active=settings.get('dry_run'))

@app.route('/settings')
@requires_auth
def settings_page():
    settings = get_settings()
    return render_template_string(MASTER_TEMPLATE, active_page='settings', page_title='Settings', app_settings=settings, version=APP_VERSION, dry_run_active=settings.get('dry_run'))

@app.route('/save_settings', methods=['POST'])
@requires_auth
def save_settings():
    settings = get_settings()
    settings['run_interval'] = int(request.form.get('run_interval', 15))
    settings['dry_run'] = 'dry_run' in request.form
    save_json(SETTINGS_FILE, settings)
    scheduler.reschedule_job('main_job', trigger='interval', minutes=settings['run_interval'])
    return redirect(url_for('settings_page'))

@app.route('/update_groups', methods=['POST'])
@requires_auth
def update_groups():
    groups = {}
    for k, v in request.form.items():
        if v.strip(): groups[k] = v.strip()
    save_json(GROUPS_FILE, groups)
    return redirect(url_for('trackers'))

@app.route('/add_rule', methods=['POST'])
@requires_auth
def add_rule():
    rules = load_json(RULES_FILE, [])
    rules.append({
        'group_id': request.form.get('group_id'),
        'label': request.form.get('label'),
        'time_metric': request.form.get('time_metric'),
        'max_hours': float(request.form.get('max_hours', 0)),
        'delete_data': True
    })
    save_json(RULES_FILE, rules)
    return redirect(url_for('rules'))

@app.route('/delete_rule/<int:index>', methods=['POST'])
@requires_auth
def delete_rule(index):
    rules = load_json(RULES_FILE, [])
    if 0 <= index < len(rules): rules.pop(index)
    save_json(RULES_FILE, rules)
    return redirect(url_for('rules'))

@app.route('/run_now', methods=['POST'])
@requires_auth
def run_now():
    process_torrents("Manual")
    return redirect(url_for('view_logs'))

@app.route('/manifest.json')
def manifest():
    return app.response_class(response=json.dumps({"name": "Delegatarr", "display": "standalone", "start_url": "/", "theme_color": "#0f172a"}), mimetype='application/json')

@app.route('/favicon.ico')
def favicon():
    if os.path.exists('/config/logo.png'): return send_from_directory('/config', 'logo.png')
    return "", 404

if __name__ == '__main__':
    download_default_logo()
    s = get_settings()
    apply_timezone(s.get('timezone', 'UTC'))
    scheduler.add_job(id='main_job', func=process_torrents, trigger='interval', minutes=s.get('run_interval', 15))
    scheduler.start()
    write_log(f"Delegatarr Secure Started. User: {APP_USER}")
    serve(app, host='0.0.0.0', port=5555)
