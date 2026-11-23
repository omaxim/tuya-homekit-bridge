# tuya-homekit-bridge

Expose Tuya / Smart Life Wi‑Fi devices to **Apple HomeKit** — locally, without the
Tuya cloud, as a single small binary.

```
Tuya device ⇄ (LAN, protocol 3.5, AES‑GCM) ⇄ tuya-homekit-bridge ⇄ HomeKit ⇄ HomePod / Apple TV hub ⇄ iPhone, Siri
```

It was built to control an electric convector heater (Klarstein Bansin Smart) from the
Home app and regulate it with the temperature sensor in a HomePod, but the mapping
between Tuya data points and HomeKit accessories is plain JSON, so it works for any
Tuya device you can describe as switches and temperature sensors.

## Features

- **Local only** – talks to devices over the LAN using the Tuya 3.5 protocol; the cloud
  is only needed once, to obtain the device's local key.
- **Config‑driven** – map any data point (DP) combination to a HomeKit switch, read
  temperatures, and keep DPs pinned to a value with `enforce` rules.
- **Auto‑discovery** – leave `ip` empty and the bridge finds the device on the LAN, and
  finds it again if its address changes.
- **Small and portable** – one static Go binary (~2.4 MB compressed), runs on macOS,
  Linux, Raspberry Pi and OpenWrt routers. An OpenWrt recipe that runs the bridge from
  RAM on routers with only 8 MB of flash is included.
- **Thermostat (optional)** – regulates a device in stages (off / heat / boost) from a
  room temperature read directly from a HomePod's sensor (the bridge pairs with it as
  a limited HomeKit controller, see [docs/homepod-sensor.md](docs/homepod-sensor.md))
  or pushed over HTTP. Real thermostat with a slider in the Home app, failsafe when the temperature
  stops arriving, see [docs/homepod-thermostat.md](docs/homepod-thermostat.md).
- **Xiaomi / Yeelight bulbs (optional)** – bulbs paired in Mi Home are controlled locally
  over the miIO protocol (on/off, brightness, white temperature, colour), see
  [docs/xiaomi-lights.md](docs/xiaomi-lights.md).
- **Local web UI (optional)** – a password‑protected page with on/off buttons for
  colleagues or family members without Apple devices, see [docs/web-ui.md](docs/web-ui.md).
- **Multiple accessories per device, multiple devices per bridge.**

## Requirements

- A Tuya device that speaks **local protocol 3.5** (most devices sold since ~2023; check
  with `-discover`). Protocols 3.1–3.4 are not supported yet.
- The device's **id** and **local key** (see [docs/getting-local-keys.md](docs/getting-local-keys.md)).
- A machine on the same LAN that is always on (Mac, Linux box, Raspberry Pi, OpenWrt router).
- For remote access and automations: a HomeKit home hub (HomePod, HomePod mini or Apple TV).

## Quick start

```bash
# 1. Install (or download a binary from the Releases page)
go install github.com/omaxim/tuya-homekit-bridge/cmd/tuya-homekit-bridge@latest

# 2. Find your devices on the LAN
tuya-homekit-bridge -discover 15s

# 3. Get the local key (one‑time, via the Smart Life app)
python3 tools/fetch_keys.py <your Smart Life user code>

# 4. Write a config (start from examples/) and look at the device's data points
cp examples/heater-klarstein-bansin-smart.json config.json
tuya-homekit-bridge -config config.json -probe

# 5. Run it
tuya-homekit-bridge -config config.json
```

Then in the Home app: **+ → Add Accessory → More options…**, pick the bridge, confirm
*Add Anyway* and enter the setup code from your config.

## Configuration

```json
{
  "name": "Heater Bridge",
  "pin": "12345678",
  "storage": "db",
  "poll_interval": "10s",
  "web": { "listen": ":8080", "password": "choose-a-password", "lang": "en" },
  "devices": [
    {
      "name": "Heater",
      "id": "<tuya device id>",
      "ip": "",
      "local_key": "<16 character local key>",
      "accessories": [
        { "type": "switch", "name": "Heater",
          "state": { "1": true },
          "on":    { "2": 50, "1": true },
          "on_then": { "4": "low" },
          "off":   { "1": false } },
        { "type": "switch", "name": "Heater boost",
          "state": { "1": true, "4": "high" },
          "on":    { "2": 50, "4": "high", "1": true },
          "off":   { "4": "low" } }
      ],
      "enforce": [ { "when": { "1": true }, "set": { "2": 50 } } ]
    }
  ]
}
```

See [docs/configuration.md](docs/configuration.md) for every option and
[docs/finding-data-points.md](docs/finding-data-points.md) for how to work out what each
DP of your device does.

## Recipes

- [Room thermostat with a HomePod sensor](docs/homepod-thermostat.md) – the bridge
  regulates off/low/high from the room temperature (or simple threshold automations).
- [Read the HomePod temperature sensor directly](docs/homepod-sensor.md) – one‑time
  pairing from an encrypted iPhone backup (`tools/hapkeys`, `cmd/hap-pair`).
- [Run on an OpenWrt router](docs/openwrt.md) – including 8 MB flash devices
  (bridge downloaded to RAM at boot, verified by SHA‑256), WireGuard for remote admin.
- [Run as a service on Linux / Raspberry Pi / macOS](docs/running-as-a-service.md).

## Command line

| Flag | Description |
|---|---|
| `-config file` | Configuration file (default `config.json`). |
| `-probe` | Print current data points of all configured devices and exit. |
| `-discover 15s` | List Tuya 3.5 devices on the LAN and exit. |
| `-version` | Print version. |

## Security notes

- The local key gives full control of the device. Keep `config.json` private
  (`chmod 600`) and never commit it — `.gitignore` already excludes it.
- The `storage` directory holds the HomeKit pairing keys. Back it up if you want to move
  the bridge to another machine without re‑pairing.
- HomeKit traffic is end‑to‑end encrypted by the HAP protocol; Tuya LAN traffic is
  AES‑GCM encrypted with a per‑connection session key.

## Credits

- Tuya protocol details ported from [tinytuya](https://github.com/jasonacox/tinytuya) by Jason Cox.
- HomeKit Accessory Protocol by [brutella/hap](https://github.com/brutella/hap).

Not affiliated with Tuya, Apple or any device manufacturer. Use at your own risk —
especially with heaters: keep the device's own safety features (overheat protection,
tip‑over switch) intact.

## License

[MIT](LICENSE)
