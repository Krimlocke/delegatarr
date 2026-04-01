import os
import json
import time
import urllib.request
from datetime import datetime, timedelta
from flask import Flask, render_template_string, request, redirect, url_for, send_from_directory, send_file
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

# --- HELPER FUNCTIONS ---
def load_json(filepath, default_val):
    if os.path.exists(filepath):
        try:
            with open(filepath, 'r') as f:
                return json.load(f)
        except json.JSONDecodeError:
            write_log(f"System Warning: {filepath} is corrupted or empty. Loading defaults.")
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
        'tracker_mode': 'all'
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
                    if not line or line.startswith('#'): 
                        continue
                    parts = line.split(':')
                    if len(parts) >= 2:
                        if user and parts[0] == user:
                            return parts[0], parts[1]
                        elif not user and (parts[0] == 'localclient' or (len(parts) >= 3 and parts[2] == '10')):
                            return parts[0], parts[1]
        except Exception as e:
            print(f"Error reading auth file: {e}")
            write_log(f"System Error: Failed to parse auth file at {DELUGE_AUTH_FILE}")
    
    if not user:
        user = 'localclient'
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
            if lbl:
                labels.add(lbl)
            
            trackers_list = [t.get(b'url', b'').decode('utf-8', 'ignore') for t in data.get(b'trackers', []) if t.get(b'url')]
            
            if tracker_mode == 'top' and trackers_list:
                trackers_list = [trackers_list[0]]
                
            for raw_url in trackers_list:
                domain = raw_url.split('/')[2] if '//' in raw_url else raw_url
                summary[domain] = summary.get(domain, 0) + 1
                
        return summary, sorted(list(labels))
    except Exception as e:
        print(f"Deluge Error: {e}")
        return {}, []
    finally:
        if client and client.connected:
            client.disconnect()

def process_torrents(run_type="Scheduled"):
    groups = load_json(GROUPS_FILE, {})
    rules = load_json(RULES_FILE, [])
    
    if not rules or not groups:
        write_log(f"{run_type} Engine Run: Skipped. No tags or rules are configured yet.")
        return

    client = None
    try:
        client = get_deluge_client()
        fields = ['name', 'trackers', 'label', 'seeding_time', 'time_added', 'state']
        torrents = client.call('core.get_torrents_status', {}, fields)
        current_time = time.time()
        settings = get_settings()
        tracker_mode = settings.get('tracker_mode', 'all')
        
        removed_count = 0  
        
        for rule in rules:
            target_group = rule.get('group_id', '')
            target_label = rule.get('label', '')
            target_state = rule.get('target_state', 'All')
            time_metric = rule.get('time_metric', 'seeding_time')
            
            try:
                min_torrents = int(rule.get('min_torrents', rule.get('min_keep', 0)))
            except (ValueError, TypeError):
                min_torrents = 0
                
            try:
                rule_max_hours = float(rule.get('max_hours', 0))
            except (ValueError, TypeError):
                rule_max_hours = 0.0

            sort_order = rule.get('sort_order', 'oldest_added')
            if sort_order == 'oldest_first': sort_order = 'oldest_added'
            if sort_order == 'newest_first': sort_order = 'newest_added'
            
            matching_torrents = []
            
            for tid, data in torrents.items():
                name = data.get(b'name', b'').decode('utf-8', 'ignore')
                label = data.get(b'label', b'').decode('utf-8', 'ignore')
                state = data.get(b'state', b'').decode('utf-8', 'ignore')
                
                seeding_hours = int(data.get(b'seeding_time') or 0) / 3600.0
                time_added = int(data.get(b'time_added') or 0)
                
                if target_state != 'All' and state != target_state:
                    continue
                
                trackers_list = [t.get(b'url', b'').decode('utf-8', 'ignore') for t in data.get(b'trackers', []) if t.get(b'url')]
                if not trackers_list: continue
                
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
                        trigger_value = (current_time - time_added) / 3600.0
                    else:
                        trigger_value = seeding_hours
                        
                    matching_torrents.append({
                        'id': tid,
                        'name': name,
                        'seeding_hours': seeding_hours,
                        'time_added': time_added,
                        'trigger_value': trigger_value
                    })
            
            if not matching_torrents:
                continue
                
            if sort_order == 'oldest_added':
                matching_torrents.sort(key=lambda x: x['time_added'], reverse=True) 
            elif sort_order == 'newest_added':
                matching_torrents.sort(key=lambda x: x['time_added'], reverse=False)
            elif sort_order == 'longest_seeding':
                matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=False)
            elif sort_order == 'shortest_seeding':
                matching_torrents.sort(key=lambda x: x['seeding_hours'], reverse=True)
                
            if min_torrents > 0:
                candidates_for_removal = matching_torrents[min_torrents:]
            else:
                candidates_for_removal = matching_torrents
                
            for t in candidates_for_removal:
                if t['trigger_value'] >= rule_max_hours:
                    try:
                        client.call('core.remove_torrent', t['id'], rule['delete_data'])
                        write_log(f"Rule Matched! Removed: '{t['name']}' (Tag: {target_group}, State: {target_state}, Metric: {time_metric}, Delete Data: {rule['delete_data']})")
                        removed_count += 1
                    except Exception as del_err:
                        write_log(f"Failed to remove '{t['name']}': {del_err}")
        
        if removed_count == 0:
            write_log(f"{run_type} Engine Run: Checked Deluge, no torrents met removal criteria.")
        else:
            write_log(f"{run_type} Engine Run: Completed. Successfully removed {removed_count} torrent(s).")
                    
    except Exception as e:
        write_log(f"Background Task Error: {e}")
    finally:
        if client and client.connected:
            client.disconnect()

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
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
    <style>
        :root {
            --bg-main: #0f172a; --bg-sidebar: #1e293b; --bg-card: #1e293b; --bg-input: #0f172a;
            --text-main: #f8fafc; --text-muted: #94a3b8; --accent: #6366f1; --accent-hover: #4f46e5;
            --border-color: #334155; --danger: #ef4444; --danger-hover: #dc2626; --success: #10b981;
        }
        * { box-sizing: border-box; }
        body { font-family: 'Inter', sans-serif; margin: 0; background-color: var(--bg-main); color: var(--text-main); display: flex; height: 100vh; overflow: hidden; }

        /* DESKTOP SIDEBAR */
        .sidebar { 
            width: 260px; 
            flex-shrink: 0; 
            background-color: var(--bg-sidebar); 
            display: flex; 
            flex-direction: column; 
            border-right: 1px solid var(--border-color); 
            z-index: 1000; 
            transition: width 0.25s ease, left 0.3s ease;
            overflow: hidden; 
        }
        
        .brand { 
            display: flex; 
            align-items: center; 
            padding: 20px 15px; 
            height: 78px; 
            border-bottom: 1px solid var(--border-color); 
            white-space: nowrap; 
            overflow: hidden; 
        }
        
        .brand-logo { width: 32px; height: 32px; border-radius: 4px; flex-shrink: 0; object-fit: contain; }
        .brand-text { font-size: 20px; font-weight: 700; letter-spacing: -0.5px; margin-left: 12px; }
        
        .nav-menu { display: flex; flex-direction: column; padding: 20px 15px; gap: 8px; flex-grow: 1; overflow-x: hidden; }
        
        .nav-link { display: flex; align-items: center; padding: 12px 16px; color: var(--text-muted); text-decoration: none; border-radius: 8px; font-weight: 500; transition: all 0.2s ease; cursor: pointer; border: none; background: none; font-size: 15px; text-align: left; white-space: nowrap; }
        .nav-link:hover { background-color: rgba(255, 255, 255, 0.05); color: var(--text-main); }
        .nav-link.active { background: linear-gradient(90deg, rgba(99,102,241,0.15) 0%, rgba(99,102,241,0) 100%); color: var(--accent); border-left: 3px solid var(--accent); border-radius: 0 8px 8px 0; }
        
        .nav-icon { width: 22px; height: 22px; margin-right: 12px; flex-shrink: 0; }
        
        .nav-action { background-color: var(--accent); color: white; font-weight: 600; text-align: center; justify-content: center; margin-top: 15px; }
        .nav-action:hover { background-color: var(--accent-hover); color: white; }
        .version-tag { padding: 15px 25px; font-size: 12px; color: var(--text-muted); border-top: 1px solid var(--border-color); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

        /* DESKTOP SIDEBAR COLLAPSED STATE */
        body.sidebar-collapsed .sidebar { width: 72px; }
        body.sidebar-collapsed .brand { justify-content: center; padding: 20px 0; }
        body.sidebar-collapsed .nav-text,
        body.sidebar-collapsed .brand-text,
        body.sidebar-collapsed .version-tag { display: none !important; }
        body.sidebar-collapsed .nav-link { justify-content: center; padding: 12px 0; }
        body.sidebar-collapsed .nav-icon { margin-right: 0; }
        
        .main-content { flex-grow: 1; padding: 0 40px 40px 40px; overflow-y: auto; position: relative; }
        
        .top-header { display: flex; align-items: center; gap: 15px; padding: 25px 0; margin-bottom: 20px; position: relative; z-index: 1050; }
        .page-header { font-size: 28px; font-weight: 700; margin: 0; letter-spacing: -0.5px; }
        
        .btn-toggle-sidebar { background: none; border: none; padding: 8px; cursor: pointer; color: var(--text-muted); border-radius: 6px; transition: all 0.2s ease; display: flex; align-items: center; justify-content: center; }
        .btn-toggle-sidebar:hover { color: var(--text-main); background: rgba(255,255,255,0.05); }
        .btn-toggle-sidebar svg { width: 24px; height: 24px; }
        
        body.sidebar-collapsed .icon-collapse { display: none; }
        body:not(.sidebar-collapsed) .icon-expand { display: none; }

        .card { background-color: var(--bg-card); border-radius: 12px; border: 1px solid var(--border-color); padding: 24px; margin-bottom: 30px; }
        .card-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; flex-wrap: wrap; gap: 15px; }
        .card-title { font-size: 18px; font-weight: 600; margin: 0; }

        .table-wrapper { width: 100%; overflow-x: auto; -webkit-overflow-scrolling: touch; border-radius: 6px; }
        table { width: 100%; border-collapse: collapse; font-size: 14px; min-width: 600px; }
        th { text-align: left; padding: 12px 16px; color: var(--text-muted); font-weight: 500; border-bottom: 2px solid var(--border-color); white-space: nowrap; }
        td { padding: 16px; border-bottom: 1px solid var(--border-color); color: var(--text-main); }
        tr:last-child td { border-bottom: none; }
        tr:hover td { background-color: rgba(255, 255, 255, 0.02); }

        input[type="text"], input[type="number"], select { padding: 10px 14px; border: 1px solid var(--border-color); border-radius: 6px; background: var(--bg-input); color: var(--text-main); font-family: 'Inter', sans-serif; font-size: 14px; }
        input[type="file"] { padding: 6px 10px; border: 1px dashed var(--border-color); background: rgba(255,255,255,0.02); color: var(--text-muted); cursor: pointer; border-radius: 6px; font-family: 'Inter', sans-serif; font-size: 13px; width: 100%;}
        input:focus, select:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 2px rgba(99, 102, 241, 0.2); }
        option { background-color: var(--bg-main); color: var(--text-main); }

        .btn { background-color: var(--border-color); color: var(--text-main); padding: 10px 16px; border: none; border-radius: 6px; cursor: pointer; font-weight: 600; transition: all 0.2s ease; text-decoration: none; display: inline-flex; align-items: center; justify-content: center; font-family: 'Inter', sans-serif; font-size: 14px; white-space: nowrap;}
        .btn:hover { background-color: #475569; }
        .btn-primary { background-color: var(--accent); color: white; }
        .btn-primary:hover { background-color: var(--accent-hover); }
        .btn-danger { background-color: var(--danger); color: white; }
        .btn-danger:hover { background-color: var(--danger-hover); }
        .btn-danger-dark { background-color: #991b1b; color: white; }
        .btn-danger-dark:hover { background-color: #7f1d1d; }

        .form-row { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; }
        .status-badge-yes { color: var(--danger); font-weight: bold; background: rgba(239, 68, 68, 0.1); padding: 4px 8px; border-radius: 4px; }
        .status-badge-no { color: var(--success); font-weight: bold; background: rgba(16, 185, 129, 0.1); padding: 4px 8px; border-radius: 4px; }
        
        .settings-label { display: block; font-weight: 500; margin-bottom: 8px; color: var(--text-muted); }
        .settings-group { margin-bottom: 25px; }
        
        .data-management-row { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 15px; }
        .data-actions { display: flex; gap: 10px; align-items: center; flex-wrap: wrap; }
        
        /* MOBILE OVERLAY BACKGROUND */
        .mobile-overlay { display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.6); z-index: 1990; backdrop-filter: blur(2px); }

        /* RESPONSIVE DESIGN FOR MOBILE DEVICES */
        @media (max-width: 768px) {
            body { flex-direction: column; }
            
            /* Sidebar becomes a sliding drawer */
            .sidebar { position: fixed; top: 0; bottom: 0; left: -260px; width: 260px !important; box-shadow: 4px 0 15px rgba(0,0,0,0.5); z-index: 2000; transition: left 0.3s ease; }
            body.mobile-sidebar-open .sidebar { left: 0; }
            body.mobile-sidebar-open .mobile-overlay { display: block; }
            
            /* Show text on mobile sidebar */
            body.sidebar-collapsed .nav-text, body.sidebar-collapsed .brand-text, body.sidebar-collapsed .version-tag { display: block !important; }
            body.sidebar-collapsed .nav-link { justify-content: flex-start; padding: 12px 16px; }
            body.sidebar-collapsed .nav-icon { margin-right: 12px; }
            body.sidebar-collapsed .brand { justify-content: flex-start; padding: 20px 15px; }
            
            /* Main Content Adjustments */
            .main-content { padding: 0 15px 30px 15px; width: 100%; }
            .top-header { padding: 15px 0; margin-bottom: 10px; }
            .page-header { font-size: 22px; }
            .card { padding: 15px; }
            
            /* Make Tables Swipeable */
            table { display: block; overflow-x: auto; white-space: nowrap; -webkit-overflow-scrolling: touch; }
            
            /* Stack Forms & Buttons vertically for thumbs */
            .form-row { flex-direction: column; align-items: stretch; gap: 10px; }
            .form-row input, .form-row select, .form-row button, .form-row label { width: 100% !important; margin-left: 0 !important; }
            
            .data-management-row, .data-actions { flex-direction: column; width: 100%; align-items: stretch; }
            .data-actions form { display: flex; flex-direction: column; width: 100%; gap: 10px; }
            .btn { width: 100%; justify-content: center; }
            #trackerFilter, .tag-input { width: 100% !important; }
        }
</style>
</head>
<body>
    <script>
        if (localStorage.getItem('sidebarCollapsed') === 'true' && window.innerWidth > 768) {
            document.body.classList.add('sidebar-collapsed');
        }
    </script>
    
    <div class="mobile-overlay" id="mobileOverlay"></div>

    <div class="sidebar" id="sidebar">
        <div class="brand">
            <img src="{{ url_for('favicon') }}?v={{ version }}" class="brand-logo" alt="Logo">
            <span class="brand-text">Delegatarr</span>
        </div>
        
        <div class="nav-menu">
            <a href="{{ url_for('trackers') }}" class="nav-link {% if active_page == 'trackers' %}active{% endif %}" title="Tracker Config">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" /></svg>
                <span class="nav-text">Tracker Config</span>
            </a>
            <a href="{{ url_for('rules') }}" class="nav-link {% if active_page == 'rules' %}active{% endif %}" title="Removal Rules">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75" /></svg>
                <span class="nav-text">Removal Rules</span>
            </a>
            <a href="{{ url_for('view_logs') }}" class="nav-link {% if active_page == 'logs' %}active{% endif %}" title="Activity Logs">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" /></svg>
                <span class="nav-text">Activity Logs</span>
            </a>
            <a href="{{ url_for('settings_page') }}" class="nav-link {% if active_page == 'settings' %}active{% endif %}" title="Settings">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>
                <span class="nav-text">Settings</span>
            </a>
            
            <div style="flex-grow: 1;"></div>
            
            <form action="{{ url_for('run_now') }}" method="POST" style="margin: 0;">
                <input type="hidden" name="return_url" value="{{ request.path }}">
                <button type="submit" class="nav-link nav-action" title="Run Engine Now">
                    <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M5.25 5.653c0-.856.917-1.398 1.667-.986l11.54 6.348a1.125 1.125 0 010 1.971l-11.54 6.347c-.75.412-1.667-.13-1.667-.986V5.653z" /></svg>
                    <span class="nav-text">Run Engine</span>
                </button>
            </form>
        </div>
        <div class="version-tag">v{{ version }}</div>
    </div>

    <div class="main-content">
        
        <div class="top-header">
            <button type="button" class="btn-toggle-sidebar" id="sidebarToggle" title="Toggle Sidebar">
                <svg class="icon-collapse" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 3v18h16.5V3H3.75zM9 3v18m5.25-12l-3 3 3 3" />
                </svg>
                <svg class="icon-expand" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M3.75 6.75h16.5M3.75 12h16.5m-16.5 5.25h16.5" />
                </svg>
            </button>
            <h1 class="page-header">{{ page_title }}</h1>
        </div>
        
        {% if active_page == 'trackers' %}
        <div class="card">
            <form action="{{ url_for('update_groups') }}" method="POST">
                <div class="card-header">
                    <h3 class="card-title">Assign Tags to Trackers</h3>
                    <select id="trackerFilter" onchange="filterTrackers()" style="width: 200px; padding: 6px 10px;">
                        <option value="all">Show All Trackers</option>
                        <option value="untagged">Show Not Tagged</option>
                        <option value="tagged">Show Tagged</option>
                    </select>
                </div>
                
                <div class="table-wrapper">
                    <table id="trackerTable">
                        <tr>
                            <th>Tracker Domain</th>
                            <th>Active Torrents</th>
                            <th>Tag Assignment</th>
                        </tr>
                        {% for tracker, count in tracker_summary.items() %}
                        <tr class="tracker-row">
                            <td style="font-weight: 500;">{{ tracker }}</td>
                            <td>{{ count }}</td>
                            <td><input type="text" class="tag-input" name="{{ tracker }}" value="{{ groups.get(tracker, '') }}" placeholder="Assign Tag..." style="width: 200px;"></td>
                        </tr>
                        {% else %}
                        <tr><td colspan="3" style="text-align: center; color: var(--text-muted); padding: 30px;">No active torrents found in Deluge.</td></tr>
                        {% endfor %}
                    </table>
                </div>
                <div style="margin-top: 20px; display: flex; justify-content: flex-end;">
                    <button type="submit" class="btn btn-primary">Save Tags</button>
                </div>
            </form>
        </div>
        <script>
            function filterTrackers() {
                try {
                    const select = document.getElementById('trackerFilter');
                    if (!select) return;
                    const filter = select.value;
                    localStorage.setItem('trackerFilterPref', filter);
                    const rows = document.querySelectorAll('.tracker-row');
                    rows.forEach(row => {
                        const input = row.querySelector('.tag-input');
                        if (!input) return;
                        const isTagged = input.value.trim() !== '';
                        if (filter === 'all') row.style.display = '';
                        else if (filter === 'tagged' && isTagged) row.style.display = '';
                        else if (filter === 'untagged' && !isTagged) row.style.display = '';
                        else row.style.display = 'none';
                    });
                } catch(e) { console.error('Filter error:', e); }
            }
            window.addEventListener('DOMContentLoaded', () => {
                setTimeout(() => {
                    try {
                        const savedFilter = localStorage.getItem('trackerFilterPref');
                        const select = document.getElementById('trackerFilter');
                        if (savedFilter && select) { select.value = savedFilter; }
                        filterTrackers();
                    } catch(e) {}
                }, 50);
            });
        </script>

        {% elif active_page == 'rules' %}
        <div class="card">
            <div class="card-header">
                <h3 class="card-title">Create New Rule</h3>
            </div>
            <form action="{{ url_for('add_rule') }}" method="POST" style="margin-bottom: 30px; padding-bottom: 25px; border-bottom: 1px solid var(--border-color);">
                <div class="form-row">
                    <input list="tagList" name="group_id" placeholder="Target Tag" style="width: 120px;" required autocomplete="off">
                    <datalist id="tagList">{% for tag in unique_tags %}<option value="{{ tag }}">{% endfor %}</datalist>

                    <input list="labelList" name="label" placeholder="Deluge Label" style="width: 120px;" required autocomplete="off">
                    <datalist id="labelList">{% for label in unique_labels %}<option value="{{ label }}">{% endfor %}</datalist>
                    
                    <select name="target_state" style="width: 120px;">
                        <option value="All">State: All</option>
                        <option value="Seeding">Seeding</option>
                        <option value="Paused">Paused</option>
                        <option value="Downloading">Downloading</option>
                    </select>
                    
                    <select name="time_metric" style="width: 170px;">
                        <option value="seeding_time">Seeding Time ></option>
                        <option value="time_added">Time Since Added ></option>
                    </select>
                    
                    <input type="number" name="max_hours" placeholder="Hours" step="any" style="width: 80px;" required>
                    <input type="number" name="min_torrents" placeholder="Min Keep" style="width: 90px;" value="0" required>
                    
                    <select name="sort_order" style="width: 200px;">
                        <option value="oldest_added">Remove Oldest Added</option>
                        <option value="newest_added">Remove Newest Added</option>
                        <option value="longest_seeding">Remove Longest Seeding</option>
                        <option value="shortest_seeding">Remove Shortest Seeding</option>
                    </select>
                    
                    <label style="display: flex; align-items: center; gap: 8px; font-weight: 500;">
                        <input type="checkbox" name="delete_data" value="yes" style="width: 18px; height: 18px; accent-color: var(--danger);"> 
                        Delete Data
                    </label>
                    
                    <button type="submit" class="btn btn-primary" style="margin-left: auto;">+ Add Rule</button>
                </div>
            </form>

            <h3 class="card-title" style="margin-bottom: 15px;">Active Rules</h3>
            <div class="table-wrapper">
                <table>
                    <tr>
                        <th>Tag</th><th>Label</th><th>State</th><th>Metric</th><th>Threshold</th><th>Min Keep</th><th>Sorting Priority</th><th>Del Data?</th><th></th>
                    </tr>
                    {% for rule in rules %}
                    <tr>
                        <td><strong style="color: var(--accent);">{{ rule.group_id }}</strong></td>
                        <td>{{ rule.label }}</td>
                        <td><span style="background: rgba(255,255,255,0.05); padding: 4px 8px; border-radius: 4px;">{{ rule.get('target_state', 'All') }}</span></td>
                        <td>{{ 'Time Since Added' if rule.get('time_metric') == 'time_added' else 'Seeding Time' }}</td>
                        <td>> {{ rule.max_hours }} hrs</td>
                        <td>{{ rule.get('min_torrents', rule.get('min_keep', 0)) }}</td>
                        <td style="color: var(--text-muted);">
                            {% if rule.get('sort_order') == 'newest_added' or rule.get('sort_order') == 'newest_first' %}Newest Added
                            {% elif rule.get('sort_order') == 'longest_seeding' %}Longest Seeding
                            {% elif rule.get('sort_order') == 'shortest_seeding' %}Shortest Seeding
                            {% else %}Oldest Added{% endif %}
                        </td>
                        <td>{% if rule.delete_data %}<span class="status-badge-yes">YES</span>{% else %}<span class="status-badge-no">NO</span>{% endif %}</td>
                        <td style="text-align: right;">
                            <form action="{{ url_for('delete_rule', index=loop.index0) }}" method="POST" style="margin:0;">
                                <button type="submit" class="btn btn-danger" style="padding: 6px 10px; font-size: 12px;">Delete</button>
                            </form>
                        </td>
                    </tr>
                    {% else %}
                    <tr><td colspan="9" style="text-align: center; color: var(--text-muted); padding: 30px;">No automation rules configured yet.</td></tr>
                    {% endfor %}
                </table>
            </div>
        </div>

        {% elif active_page == 'logs' %}
        <div class="card">
            <pre style="white-space: pre-wrap; word-wrap: break-word; margin: 0; font-family: monospace; font-size: 13px; color: var(--text-muted); line-height: 1.6;">{{ log_content }}</pre>
        </div>
        
        {% elif active_page == 'settings' %}
        <div class="card" style="max-width: 650px;">
            <div class="card-header">
                <h3 class="card-title">Application Settings</h3>
            </div>
            <form action="{{ url_for('save_settings') }}" method="POST">
                
                <div class="settings-group">
                    <label class="settings-label">Engine Run Interval (Minutes)</label>
                    <p style="font-size: 13px; color: var(--text-muted); margin-top: 0; margin-bottom: 10px;">How often should Delegatarr run in the background to check the rules?</p>
                    <input type="number" name="run_interval" value="{{ app_settings.get('run_interval', 15) }}" min="1" required style="width: 150px;">
                </div>
                
                <div class="settings-group">
                    <label class="settings-label">Log Retention (Days)</label>
                    <p style="font-size: 13px; color: var(--text-muted); margin-top: 0; margin-bottom: 10px;">How many days of history should be kept in the Activity Logs?</p>
                    <input type="number" name="log_retention_days" value="{{ app_settings.get('log_retention_days', 30) }}" min="1" required style="width: 150px;">
                </div>

                <div class="settings-group">
                    <label class="settings-label">System Timezone</label>
                    <p style="font-size: 13px; color: var(--text-muted); margin-top: 0; margin-bottom: 10px;">Used to correctly timestamp logs and trigger scheduled tasks.</p>
                    <select id="tzSelect" name="timezone" style="width: 250px;">
                        </select>
                </div>
                
                <div class="settings-group">
                    <label class="settings-label">Tracker Reading Mode</label>
                    <p style="font-size: 13px; color: var(--text-muted); margin-top: 0; margin-bottom: 10px;">Should Delegatarr read all trackers attached to a torrent, or just the primary tracker?</p>
                    <select name="tracker_mode" style="width: 250px;">
                        <option value="all" {% if app_settings.get('tracker_mode', 'all') == 'all' %}selected{% endif %}>All Trackers</option>
                        <option value="top" {% if app_settings.get('tracker_mode', 'all') == 'top' %}selected{% endif %}>Primary Tracker</option>
                    </select>
                </div>
                
                <div style="border-top: 1px solid var(--border-color); padding-top: 20px; margin-top: 10px;">
                    <button type="submit" class="btn btn-primary">Save Changes</button>
                </div>
            </form>
        </div>
        
        <div class="card" style="max-width: 650px;">
            <div class="card-header">
                <h3 class="card-title">Data Management</h3>
            </div>
            
            <div class="data-management-row">
                <div class="data-actions">
                    <a href="{{ url_for('export_settings') }}" class="btn btn-primary">Export All Data</a>
                    
                    <form action="{{ url_for('import_settings') }}" method="POST" enctype="multipart/form-data" style="margin: 0; display: flex; gap: 8px;">
                        <input type="file" name="settings_file" accept=".json" required>
                        <button type="submit" class="btn">Import</button>
                    </form>
                </div>
            </div>
            
            <div class="data-actions" style="margin-top: 25px; border-top: 1px solid var(--border-color); padding-top: 20px; justify-content: space-between;">
                <div>
                    <strong style="color: var(--danger); display: block; margin-bottom: 4px;">Danger Zone</strong>
                </div>
                <div style="display: flex; gap: 10px; flex-wrap: wrap;">
                    <form action="{{ url_for('factory_reset_settings') }}" method="POST" style="margin: 0; flex-grow: 1;" onsubmit="return confirm('WARNING: Are you sure? This will wipe your App Settings and restore them to default. Your Rules and Tags will NOT be touched.');">
                        <button type="submit" class="btn btn-danger">Reset Settings</button>
                    </form>
                    
                    <form action="{{ url_for('factory_reset_all') }}" method="POST" style="margin: 0; flex-grow: 1;" onsubmit="return confirm('CRITICAL WARNING: Are you absolutely sure? This will completely WIPE ALL Settings, Rules, and Tracker Tags. This cannot be undone!');">
                        <button type="submit" class="btn btn-danger-dark" title="Delete everything and start fresh">
                            <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" style="margin-right: 6px;">
                                <circle cx="9" cy="12" r="1"/>
                                <circle cx="15" cy="12" r="1"/>
                                <path d="M8 20v2h8v-2"/>
                                <path d="m12.5 17-.5-1-.5 1h1z"/>
                                <path d="M16 20a2 2 0 0 0 1.56-3.25 8 8 0 1 0-11.12 0A2 2 0 0 0 8 20"/>
                            </svg>
                            Nuclear Option
                        </button>
                    </form>
                </div>
            </div>

        </div>
        
        <script>
            document.addEventListener('DOMContentLoaded', () => {
                const tzSelect = document.getElementById('tzSelect');
                if (tzSelect) {
                    const timeZones = Intl.supportedValuesOf('timeZone');
                    const currentTz = "{{ app_settings.get('timezone', 'UTC') }}";
                    
                    timeZones.forEach(tz => {
                        const option = document.createElement('option');
                        option.value = tz;
                        option.textContent = tz;
                        if (tz === currentTz) option.selected = true;
                        tzSelect.appendChild(option);
                    });
                }
            });
        </script>
        {% endif %}

    </div>
    
    <script>
        document.addEventListener('DOMContentLoaded', () => {
            const toggleBtn = document.getElementById('sidebarToggle');
            const mobileOverlay = document.getElementById('mobileOverlay');
            
            if (toggleBtn) {
                toggleBtn.addEventListener('click', () => {
                    if (window.innerWidth <= 768) {
                        document.body.classList.toggle('mobile-sidebar-open');
                    } else {
                        document.body.classList.toggle('sidebar-collapsed');
                        localStorage.setItem('sidebarCollapsed', document.body.classList.contains('sidebar-collapsed'));
                    }
                });
            }
            
            if (mobileOverlay) {
                mobileOverlay.addEventListener('click', () => {
                    document.body.classList.remove('mobile-sidebar-open');
                });
            }
        });
    </script>

    <script>
        if ('serviceWorker' in navigator) {
            window.addEventListener('load', () => {
                navigator.serviceWorker.register("{{ url_for('service_worker') }}")
                    .then(reg => console.log('Service Worker registered'))
                    .catch(err => console.log('Service Worker registration failed: ', err));
            });
        }
    </script>
</body>
</html>
"""

# --- ROUTES ---
@app.route('/')
def index():
    return redirect(url_for('trackers'))

@app.route('/trackers')
def trackers():
    summary, _ = get_dashboard_data()
    groups = load_json(GROUPS_FILE, {})
    return render_template_string(MASTER_TEMPLATE, active_page='trackers', page_title='Tracker Configuration', tracker_summary=summary, groups=groups, version=APP_VERSION)

@app.route('/rules')
def rules():
    _, unique_labels = get_dashboard_data()
    groups = load_json(GROUPS_FILE, {})
    rules = load_json(RULES_FILE, [])
    unique_tags = sorted(list(set(tag for tag in groups.values() if tag.strip())))
    return render_template_string(MASTER_TEMPLATE, active_page='rules', page_title='Removal Rules Engine', rules=rules, unique_tags=unique_tags, unique_labels=unique_labels, version=APP_VERSION)

@app.route('/logs')
def view_logs():
    log_content = "No logs generated yet."
    if os.path.exists(LOG_FILE):
        with open(LOG_FILE, 'r') as f:
            lines = f.readlines()
            lines.reverse()
            if lines:
                log_content = "".join(lines)
    return render_template_string(MASTER_TEMPLATE, active_page='logs', page_title='Activity Logs', log_content=log_content, version=APP_VERSION)

@app.route('/settings')
def settings_page():
    current_settings = get_settings()
    if 'timezone' not in current_settings:
        current_settings['timezone'] = os.environ.get('TZ', 'UTC')
    return render_template_string(MASTER_TEMPLATE, active_page='settings', page_title='Settings', app_settings=current_settings, version=APP_VERSION)

@app.route('/save_settings', methods=['POST'])
def save_settings():
    try:
        new_interval = int(request.form.get('run_interval', 15))
    except (ValueError, TypeError):
        new_interval = 15
        
    try:
        new_retention = int(request.form.get('log_retention_days', 30))
    except (ValueError, TypeError):
        new_retention = 30
        
    new_tz = request.form.get('timezone', 'UTC')
    new_tracker_mode = request.form.get('tracker_mode', 'all')
    
    settings_data = {
        'run_interval': new_interval,
        'log_retention_days': new_retention,
        'timezone': new_tz,
        'tracker_mode': new_tracker_mode
    }
    save_json(SETTINGS_FILE, settings_data)
    
    apply_timezone(new_tz)
    
    try:
        scheduler.reschedule_job('main_engine_job', trigger='interval', minutes=new_interval)
        write_log(f"System: Settings updated. Interval: {new_interval}m, TZ: {new_tz}, Tracker Mode: {new_tracker_mode}.")
    except Exception as e:
        print(f"Failed to reschedule job: {e}")

    return redirect(url_for('settings_page'))

# --- DATA MANAGEMENT ROUTES ---
@app.route('/export_settings')
def export_settings():
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
            try:
                # FIXED: Correctly decodes the uploaded byte stream into JSON text
                data = json.loads(file.read().decode('utf-8'))
                if 'settings' in data or 'rules' in data or 'groups' in data:
                    if 'settings' in data: save_json(SETTINGS_FILE, data['settings'])
                    if 'rules' in data: save_json(RULES_FILE, data['rules'])
                    if 'groups' in data: save_json(GROUPS_FILE, data['groups'])
                else:
                    save_json(SETTINGS_FILE, data)
                write_log("System: Full backup imported successfully.")
            except Exception as e:
                write_log(f"System Error: Failed to import backup file. {e}")
    return redirect(url_for('settings_page'))

@app.route('/factory_reset_settings', methods=['POST'])
def factory_reset_settings():
    default_settings = {
        'run_interval': 15,
        'log_retention_days': 30,
        'timezone': 'UTC',
        'tracker_mode': 'all'
    }
    save_json(SETTINGS_FILE, default_settings)
    write_log("System: Factory reset performed on application settings only.")
    return redirect(url_for('settings_page'))

@app.route('/factory_reset_all', methods=['POST'])
def factory_reset_all():
    default_settings = {
        'run_interval': 15,
        'log_retention_days': 30,
        'timezone': 'UTC',
        'tracker_mode': 'all'
    }
    save_json(SETTINGS_FILE, default_settings)
    save_json(RULES_FILE, [])
    save_json(GROUPS_FILE, {})
    write_log("System: CRITICAL - Full factory reset performed. All settings, rules, and tags have been wiped.")
    return redirect(url_for('settings_page'))

@app.route('/update_groups', methods=['POST'])
def update_groups():
    groups = load_json(GROUPS_FILE, {})
    for tracker, group_id in request.form.items():
        if group_id.strip():
            groups[tracker] = group_id.strip()
        elif tracker in groups and not group_id.strip():
            del groups[tracker]
    save_json(GROUPS_FILE, groups)
    return redirect(url_for('trackers'))

@app.route('/add_rule', methods=['POST'])
def add_rule():
    rules = load_json(RULES_FILE, [])
    
    try:
        max_hours = float(request.form.get('max_hours', 0))
    except (ValueError, TypeError):
        max_hours = 0.0
        
    try:
        min_torrents = int(request.form.get('min_torrents', 0))
    except (ValueError, TypeError):
        min_torrents = 0
        
    new_rule = {
        'group_id': request.form.get('group_id', '').strip(),
        'label': request.form.get('label', '').strip(),
        'target_state': request.form.get('target_state', 'All'),
        'time_metric': request.form.get('time_metric', 'seeding_time'),
        'min_torrents': min_torrents,
        'sort_order': request.form.get('sort_order', 'oldest_added'),
        'max_hours': max_hours,
        'delete_data': 'delete_data' in request.form
    }
    rules.append(new_rule)
    save_json(RULES_FILE, rules)
    return redirect(url_for('rules'))

@app.route('/delete_rule/<int:index>', methods=['POST'])
def delete_rule(index):
    rules = load_json(RULES_FILE, [])
    if 0 <= index < len(rules):
        rules.pop(index)
        save_json(RULES_FILE, rules)
    return redirect(url_for('rules'))

@app.route('/run_now', methods=['POST'])
def run_now():
    process_torrents(run_type="Manual")
    return_url = request.form.get('return_url', url_for('trackers'))
    return redirect(return_url)

# --- PWA ROUTES ---
@app.route('/manifest.json')
def manifest():
    manifest_data = {
        "name": "Delegatarr",
        "short_name": "Delegatarr",
        "start_url": "/",
        "display": "standalone",
        "background_color": "#0f172a",
        "theme_color": "#0f172a",
        "icons": [{
            "src": url_for('favicon'),
            "sizes": "192x192 512x512",
            "type": "image/png"
        }]
    }
    return app.response_class(
        response=json.dumps(manifest_data),
        status=200,
        mimetype='application/json'
    )

@app.route('/sw.js')
def service_worker():
    sw_js = """
    self.addEventListener('install', (e) => {
      self.skipWaiting();
    });
    self.addEventListener('fetch', (e) => {
      // Pass through normal network requests
    });
    """
    return app.response_class(
        response=sw_js,
        status=200,
        mimetype='application/javascript'
    )

@app.route('/favicon.ico')
def favicon():
    if os.path.exists('/config/logo.png'):
        return send_from_directory('/config', 'logo.png', mimetype='image/png')
    base_dir = os.path.dirname(os.path.abspath(__file__))
    if os.path.exists(os.path.join(base_dir, 'logo.png')):
        return send_from_directory(base_dir, 'logo.png', mimetype='image/png')
    return "", 404

if __name__ == '__main__':
    download_default_logo()
    
    boot_settings = get_settings()
    boot_interval = int(boot_settings.get('run_interval', 15))
    
    if 'timezone' in boot_settings:
        apply_timezone(boot_settings['timezone'])
    elif 'TZ' in os.environ:
        apply_timezone(os.environ['TZ'])
    
    scheduler.add_job(func=process_torrents, trigger="interval", minutes=boot_interval, id='main_engine_job')
    scheduler.add_job(func=cleanup_logs, trigger="interval", days=1)
    scheduler.start()
    
    try:
        write_log("System: Starting Waitress WSGI production server on port 5555...")
        serve(app, host='0.0.0.0', port=5555)
    except Exception as e:
        write_log(f"System Error: Failed to start web server: {e}")
    finally:
        scheduler.shutdown()
