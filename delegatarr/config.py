import os
import json
import time
import logging

# --- VERSION CONTROL ---
APP_VERSION = "2026.04.08.py"

# --- SYSTEM DIRECTORIES ---
CONFIG_DIR = '/config'
os.makedirs(CONFIG_DIR, exist_ok=True)

# --- FILE PATHS ---
LOG_FILE = os.path.join(CONFIG_DIR, 'delegatarr.log')
SECRET_KEY_FILE = os.path.join(CONFIG_DIR, 'secret.key')
GROUPS_FILE = os.path.join(CONFIG_DIR, 'groups.json')
RULES_FILE = os.path.join(CONFIG_DIR, 'rules.json')
SETTINGS_FILE = os.path.join(CONFIG_DIR, 'settings.json')
LOGO_FILE = os.path.join(CONFIG_DIR, 'logo.png')

logger = logging.getLogger(__name__)

def load_json(filepath, default_val):
    """Safely loads a JSON file, returning a default value if corrupted or missing."""
    if os.path.exists(filepath):
        try:
            with open(filepath, 'r') as f:
                return json.load(f)
        except (json.JSONDecodeError, OSError) as e:
            logger.error(f"Failed to load {filepath}: {e}")
            return default_val
    return default_val

def save_json(filepath, data):
    """Safely saves data to a JSON file using a temporary file to prevent corruption during writes."""
    tmp = filepath + '.tmp'
    try:
        with open(tmp, 'w') as f:
            json.dump(data, f, indent=4)
        os.replace(tmp, filepath)
    except OSError as e:
        logger.error(f"Failed to save {filepath}: {e}")
        # Clean up temp file on failure
        if os.path.exists(tmp):
            try:
                os.remove(tmp)
            except OSError:
                pass
        raise

def get_settings():
    """Retrieves application settings with safe fallback defaults."""
    return load_json(SETTINGS_FILE, {
        'run_interval': 15,
        'log_retention_days': 30,
        'timezone': 'UTC',
        'tracker_mode': 'all',
        'dry_run': True
    })

def apply_timezone(tz_string):
    """Applies the specified timezone to the underlying operating system environment."""
    os.environ['TZ'] = tz_string
    if hasattr(time, 'tzset'):
        time.tzset()
