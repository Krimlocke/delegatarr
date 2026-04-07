import os
import threading
import logging
import urllib.request
import ssl
import secrets
from logging.handlers import RotatingFileHandler

from flask import Flask
from flask_wtf.csrf import CSRFProtect
from waitress import serve
from apscheduler.schedulers.background import BackgroundScheduler

# Import application modules
from delegatarr.config import LOG_FILE, SECRET_KEY_FILE, CONFIG_DIR, get_settings, apply_timezone
from delegatarr.deluge import wait_for_deluge
from delegatarr.engine import process_torrents
from delegatarr.routes import bp

# --- INITIALIZE APPLICATION ---
app = Flask(__name__)

# Generate a secure API token for internal JS/Scripting communication
app.config['API_TOKEN'] = os.environ.get('API_TOKEN', secrets.token_hex(32))

csrf = CSRFProtect(app)
scheduler = BackgroundScheduler()
app.scheduler = scheduler

# --- SETUP LOGGING ---
log_formatter = logging.Formatter('[%(asctime)s] %(message)s', datefmt='%Y-%m-%d %H:%M:%S')

file_handler = RotatingFileHandler(LOG_FILE, maxBytes=10 * 1024 * 1024, backupCount=5)
file_handler.setFormatter(log_formatter)

console_handler = logging.StreamHandler()
console_handler.setFormatter(log_formatter)

# Configure the root logger to catch logs from all modules
root_logger = logging.getLogger()
root_logger.setLevel(logging.INFO)
root_logger.addHandler(file_handler)
root_logger.addHandler(console_handler)

# Silence chatty external libraries
logging.getLogger('deluge_client').setLevel(logging.WARNING)
logging.getLogger('apscheduler').setLevel(logging.WARNING)

# --- SECRET KEY MANAGEMENT ---
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

# Register the routes blueprint
app.register_blueprint(bp)

def download_default_logo():
    """Downloads a fallback logo if one is not present in the configuration directory."""
    logo_path = os.path.join(CONFIG_DIR, 'logo.png')
    logo_url = 'https://raw.githubusercontent.com/Krimlocke/delegatarr/refs/heads/main/logo.png'
    if not os.path.exists(logo_path):
        try:
            root_logger.info("System: Logo missing. Downloading default from GitHub in background...")
            ctx = ssl.create_default_context()
            req = urllib.request.Request(logo_url, headers={'User-Agent': 'Mozilla/5.0'})
            with urllib.request.urlopen(req, timeout=10, context=ctx) as response, open(logo_path, 'wb') as out_file:
                out_file.write(response.read())
            root_logger.info("System: Default logo downloaded successfully.")
        except Exception as e:
            root_logger.error(f"System Error: Failed to download default logo: {e}")

# --- STARTUP ROUTINE ---
if __name__ == '__main__':
    root_logger.info("System: Initializing Delegatarr Beta...")
    
    # 1. Apply Timezone
    boot_settings = get_settings()
    if 'timezone' in boot_settings:
        apply_timezone(boot_settings['timezone'])
    elif 'TZ' in os.environ:
        apply_timezone(os.environ['TZ'])

    # 2. Setup tasks
    threading.Thread(target=download_default_logo, daemon=True).start()
    
    root_logger.info("System: Starting pre-flight checks. Waiting for Deluge connection (This may take up to 60s)...")
    wait_for_deluge()

    # 3. Configure Background Engine
    boot_interval = int(boot_settings.get('run_interval', 15))
    scheduler.add_job(func=process_torrents, trigger="interval", minutes=boot_interval, id='main_engine_job')
    scheduler.start()

    # 4. Start Production Web Server
    try:
        root_logger.info("System: Web UI is now live and ready! (Listening on Port 5555)")
        serve(app, host='0.0.0.0', port=5555)
    except Exception as e:
        root_logger.error(f"System Error: Failed to start web server: {e}")
    finally:
        scheduler.shutdown()
