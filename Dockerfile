<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{{ page_title }} - Delegatarr</title>
    
    <link rel="manifest" href="{{ url_for('main.manifest') }}">
    <meta name="theme-color" content="#0f172a">
    <link rel="apple-touch-icon" href="{{ url_for('main.favicon') }}?v={{ version }}">
    <link rel="icon" type="image/png" href="{{ url_for('main.favicon') }}?v={{ version }}">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&display=swap" rel="stylesheet">
    
    <link rel="stylesheet" href="{{ url_for('static', filename='css/style.css') }}?v={{ version }}">
    
    <script>
        if (sessionStorage.getItem('sidebarCollapsed') === 'true' && window.innerWidth > 768) {
            document.documentElement.classList.add('sidebar-collapsed');
        }
        // Pass Jinja variables to the extracted JS
        window.APP_CONFIG = {
            swUrl: "{{ url_for('main.service_worker') }}"
        };
    </script>
</head>
<body>
    <div class="mobile-overlay" id="mobileOverlay"></div>

    <div class="sidebar" id="sidebar">
        <a href="https://github.com/Krimlocke/delegatarr" target="_blank" rel="noopener noreferrer" class="brand">
            <img src="{{ url_for('main.favicon') }}?v={{ version }}" class="brand-logo" alt="Logo">
            <span class="brand-text">Delegatarr</span>
        </a>

        <div class="nav-menu">
            <a href="{{ url_for('main.trackers') }}" class="nav-link {% if active_page == 'trackers' %}active{% endif %}" title="Tracker Config">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M8.25 6.75h12M8.25 12h12m-12 5.25h12M3.75 6.75h.007v.008H3.75V6.75zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zM3.75 12h.007v.008H3.75V12zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0zm-.375 5.25h.007v.008H3.75v-.008zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" /></svg>
                <span class="nav-text">Tracker Config</span>
            </a>
            <a href="{{ url_for('main.rules') }}" class="nav-link {% if active_page == 'rules' %}active{% endif %}" title="Removal Rules">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75" /></svg>
                <span class="nav-text">Removal Rules</span>
            </a>
            <a href="{{ url_for('main.view_logs') }}" class="nav-link {% if active_page == 'logs' %}active{% endif %}" title="Activity Logs">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M19.5 14.25v-2.625a3.375 3.375 0 00-3.375-3.375h-1.5A1.125 1.125 0 0113.5 7.125v-1.5a3.375 3.375 0 00-3.375-3.375H8.25m0 12.75h7.5m-7.5 3H12M10.5 2.25H5.625c-.621 0-1.125.504-1.125 1.125v17.25c0 .621.504 1.125 1.125 1.125h12.75c.621 0 1.125-.504 1.125-1.125V11.25a9 9 0 00-9-9z" /></svg>
                <span class="nav-text">Activity Logs</span>
            </a>
            <a href="{{ url_for('main.settings_page') }}" class="nav-link {% if active_page == 'settings' %}active{% endif %}" title="Settings">
                <svg xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24" stroke-width="1.5" stroke="currentColor" class="nav-icon"><path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>
                <span class="nav-text">Settings</span>
            </a>

            <div style="flex-grow: 1;"></div>

            <div class="conn-status {{ 'connected' if deluge_connected else 'disconnected' }}" title="Deluge {{ 'Connected' if deluge_connected else 'Unreachable' }}">
                <span class="dot"></span>
                <span class="nav-text conn-label">{{ 'Connected' if deluge_connected else 'Unreachable' }}</span>
            </div>

            <form action="{{ url_for('main.run_now') }}" method="POST" style="margin: 0;">
                <input type="hidden" name="csrf_token" value="{{ csrf_token() }}"/>
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

        {% with messages = get_flashed_messages(with_categories=true) %}
            {% if messages %}
            <div class="flash-container">
                {% for category, message in messages %}
                <div class="flash {{ category }}">
                    <span style="flex-grow: 1;">{{ message }}</span>
                    <button type="button" class="flash-close" aria-label="Close message" onclick="this.parentElement.remove()">&times;</button>
                </div>
                {% endfor %}
            </div>
            {% endif %}
        {% endwith %}

        {% block content %}{% endblock %}

    </div>

    <script src="{{ url_for('static', filename='js/main.js') }}?v={{ version }}"></script>
</body>
</html>

