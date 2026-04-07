import os
import ssl
import time
import threading
import logging
from contextlib import contextmanager
from deluge_client import DelugeRPCClient

# Set up a module-level logger (Decoupled from Flask)
logger = logging.getLogger(__name__)

# --- INFRASTRUCTURE CONFIGURATION ---
DELUGE_HOST = os.environ.get('DELUGE_HOST', '')
DELUGE_PORT = int(os.environ.get('DELUGE_PORT', 58846))
DELUGE_USER = os.environ.get('DELUGE_USER', '')
DELUGE_PASS = os.environ.get('DELUGE_PASS', '')
DELUGE_AUTH_FILE = os.environ.get('DELUGE_AUTH_FILE', '/config/deluge_auth')

# --- CACHING STRATEGY ---
_cache_lock = threading.Lock()
_deluge_status_cache = {'status': False, 'timestamp': 0}

def get_deluge_credentials():
    """Retrieves Deluge credentials from environment variables or the auth file."""
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
            logger.error(f"System Error: Failed to parse auth file at {DELUGE_AUTH_FILE}: {e}")

    if not user:
        user = 'localclient'
    return user, password

def get_deluge_client():
    """Creates and connects a Deluge RPC Client."""
    if not DELUGE_HOST:
        logger.warning("Deluge Error: DELUGE_HOST is not configured.")
        return None

    user, password = get_deluge_credentials()
    client = DelugeRPCClient(DELUGE_HOST, DELUGE_PORT, user, password)
    
    try:
        client.connect()
        return client
    except ssl.SSLEOFError:
        logger.warning("Deluge Error: Daemon is currently restarting and not ready for secure connections. Retrying later.")
        return None
    except Exception as e:
        logger.error(f"Deluge Error: {str(e)}")
        return None

@contextmanager
def deluge_session():
    """Context manager to ensure Deluge connections are safely closed after use."""
    client = None
    try:
        client = get_deluge_client()
        if client is None:
            raise ConnectionError("Could not establish Deluge connection.")
        yield client
    finally:
        if client and client.connected:
            try:
                client.disconnect()
            except Exception:
                pass

def wait_for_deluge(max_retries=12, delay_seconds=5):
    """Blocks startup until Deluge is responsive or max retries are hit."""
    if not DELUGE_HOST:
        logger.info("System: DELUGE_HOST not configured, skipping connection wait.")
        return False

    logger.info("System: Waiting for Deluge daemon to become available...")
    for attempt in range(1, max_retries + 1):
        try:
            with deluge_session() as client:
                client.call('daemon.info')
            logger.info(f"System: Deluge connection established. (Attempt {attempt}/{max_retries})")
            return True
        except Exception as e:
            err_str = str(e).lower()
            if "auth" in err_str or "login" in err_str or "password" in err_str:
                logger.error(f"System Error: Deluge authentication failed. Please check credentials. ({e})")
                break
            logger.warning(f"System: Deluge not ready yet (Attempt {attempt}/{max_retries}). Retrying in {delay_seconds}s...")
            time.sleep(delay_seconds)

    logger.warning("System Warning: Deluge did not respond in time or authentication failed. Proceeding with startup, but scheduled runs may fail.")
    return False

def get_deluge_status():
    """Pings Deluge for a status check, using a 10-second cache to prevent spamming the daemon."""
    if not DELUGE_HOST:
        return False

    global _deluge_status_cache
    
    # 1. Fast path: check cache without blocking other threads
    if time.time() - _deluge_status_cache.get('timestamp', 0) < 10:
        return _deluge_status_cache.get('status', False)

    # 2. Slow path: acquire lock and double check
    with _cache_lock:
        if time.time() - _deluge_status_cache.get('timestamp', 0) < 10:
            return _deluge_status_cache.get('status', False)
            
        # 3. Perform network call INSIDE the lock to prevent concurrent pings
        try:
            with deluge_session() as client:
                client.call('daemon.info')
            status = True
        except Exception:
            status = False
            
        # 4. Update the cache and timestamp safely inside the lock
        _deluge_status_cache['status'] = status
        _deluge_status_cache['timestamp'] = time.time()
        
        return status
