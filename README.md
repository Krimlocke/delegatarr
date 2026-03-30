Delegatarr
A lightweight, standalone web GUI and automation engine for Deluge.

If you use Deluge within your media stack and want granular, set-and-forget control over when your torrents are removed, Delegatarr is built for you. It sits alongside your existing *arr applications and connects directly to the Deluge Daemon to automate your seeding and removal strategies based on individual trackers.

Why use Delegatarr?
I wanted a modern, visual way to manage my seeding rules without relying on bulky Deluge plugins or writing complex bash scripts. Delegatarr allows you to assign custom "Tags" to the trackers attached to your torrents, and then build specific removal rules for those tags.

Key Features:

Tracker Tagging: Automatically groups your active torrents by their tracker domain, allowing you to easily assign tags (e.g., "Public", "Private-Tracker-A", "IPT").

Granular Removal Rules: Build rules based on Target Tag, Deluge Label, Torrent State (Seeding/Paused/Downloading), and Time Thresholds (Seeding Time vs. Time Since Added).

Min Keep & Sorting: Want to ensure you always keep at least 5 torrents cross-seeding? Set a "Min Keep" value and tell the engine whether to prioritize removing the oldest or newest added first.

Direct Daemon Connection: Uses Deluge's lightning-fast RPC protocol (Port 58846) rather than the slower WebUI API.

Auto-Authentication: Securely maps to your Deluge auth file to automatically log in using localclient credentials—no need to expose your web password.

Built-in Data Management: Easily export your settings, import backups, or trigger a "Nuclear Option" to wipe the slate clean and start fresh.

Installation & Unraid Setup
Delegatarr is aiming to be available in the Community Applications store. When setting up the container, pay close attention to these parameters:

Deluge IP: The local IP address of your Deluge container.

Deluge Port: This MUST be the internal Daemon port (default: 58846), NOT your WebUI port (8112). Delegatarr communicates via high-speed RPC.

Deluge Auth File (Path Mapping): This is a read-only bridge. Map the Host Path to your Deluge container's auth file (e.g., /mnt/user/appdata/deluge/auth). This allows Delegatarr to automatically grab the background credentials to connect. It must be the file, not just the folder.

Quick Start Guide
Tag Your Trackers: Open the Delegatarr WebUI. It will automatically scan Deluge and list all active tracker domains. Assign a custom Tag to the trackers you want to automate.

Create Rules: Head to the "Removal Rules" tab. Select a Tag you just created, set your thresholds (e.g., Remove if Seeding Time > 168 hours), choose whether to delete the data, and click Add Rule.

Let it Run: Delegatarr runs quietly in the background (default check every 15 minutes). You can monitor exactly what it removes in the Activity Logs tab.
