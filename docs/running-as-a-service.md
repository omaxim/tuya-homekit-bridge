# Running as a service

The bridge must run on a machine **in the same LAN** as the Tuya device and the HomeKit
hub (multicast DNS is used for discovery and does not cross routers).

## Linux / Raspberry Pi (systemd)

```bash
sudo install -m 755 tuya-homekit-bridge /usr/local/bin/
sudo install -d -m 700 /etc/tuya-homekit-bridge
sudo install -m 600 config.json /etc/tuya-homekit-bridge/config.json
# set "storage": "/var/lib/tuya-homekit-bridge" in the config
```

`/etc/systemd/system/tuya-homekit-bridge.service`:

```ini
[Unit]
Description=Tuya to HomeKit bridge
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=/usr/local/bin/tuya-homekit-bridge -config /etc/tuya-homekit-bridge/config.json
StateDirectory=tuya-homekit-bridge
Restart=always
RestartSec=5
DynamicUser=yes

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now tuya-homekit-bridge
journalctl -u tuya-homekit-bridge -f
```

With `DynamicUser=yes` make the config readable by the service, e.g. by placing it in
`/etc/tuya-homekit-bridge` with mode `644` inside a directory with mode `755`, or drop
`DynamicUser` and run as a dedicated user.

## macOS (launchd)

`~/Library/LaunchAgents/com.github.tuya-homekit-bridge.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.github.tuya-homekit-bridge</string>
  <key>ProgramArguments</key><array>
    <string>/usr/local/bin/tuya-homekit-bridge</string>
    <string>-config</string><string>/usr/local/etc/tuya-homekit-bridge/config.json</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.github.tuya-homekit-bridge.plist
```

Prevent the Mac from sleeping, otherwise the accessory will show *No Response*.

## Moving to another machine without re‑pairing

Copy the `storage` directory together with the config. The pairing (keys, accessory id)
lives there; the Home app will not notice the move.

Migrating from a HAP‑python based bridge? See `tools/migrate_hap_python_state.py`.
