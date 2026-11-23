# Xiaomi / Yeelight bulbs (miIO)

Bulbs paired through the **Mi Home** app (models `yeelink.light.*`) speak the Xiaomi
miIO protocol on the LAN (UDP 54321, AES‑encrypted with a per‑device **token**). The
bridge exposes them to HomeKit as colour light bulbs (on/off, brightness, colour
temperature, hue/saturation) and shows them in the web UI.

## Get the token

The token is only known to the Xiaomi cloud. Use
[Xiaomi-cloud-tokens-extractor](https://github.com/PiotrMachowski/Xiaomi-cloud-tokens-extractor)
(supports QR‑code login and two‑factor authentication); the older `miiocli cloud`
command often fails with "Access denied" on accounts that require 2FA.

It prints name, IP, token and model for every device. Keep the token private.

## Configuration

```json
"lights": [
  { "name": "Studio lamp", "type": "yeelight", "ip": "192.168.1.50",
    "token": "<32 hex characters>", "color": true }
]
```

| Key | Description |
|---|---|
| `name` | Name in the Home app and the web UI. |
| `type` | `yeelight` (the only type so far). |
| `ip` | The bulb's IP – give it a fixed DHCP lease. |
| `token` | 32‑character miIO token. |
| `color` | `true` for RGB bulbs (adds colour swatches / hue+saturation). |

Lights use fixed HomeKit accessory IDs starting at 200.

Tested with `yeelink.light.color5` (firmware 2.0.8). Re‑pairing the bulb in Mi Home
creates a new token.
