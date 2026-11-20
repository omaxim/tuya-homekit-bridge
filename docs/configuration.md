# Configuration reference

The bridge reads one JSON file (`-config`, default `config.json`).

## Top level

| Key | Default | Description |
|---|---|---|
| `name` | `"Tuya Bridge"` | Bridge name shown while pairing. |
| `pin` | – (required) | 8‑digit HomeKit setup code, e.g. `"12345678"` → entered as `123-45-678`. Avoid trivial codes like `12345678` in real use. |
| `storage` | `"db"` | Directory where HomeKit pairing data is stored. Must be persistent and private. |
| `poll_interval` | `"10s"` | How often device state is read. |
| `interfaces` | all | Restrict HomeKit advertising to these interfaces, e.g. `["br-lan"]`. |
| `web` | off | Optional local web UI, see [web-ui.md](web-ui.md). |
| `thermostat` | off | Optional thermostat fed by an external temperature, see [homepod-thermostat.md](homepod-thermostat.md). |
| `devices` | – | List of devices (below). |

## Device

| Key | Description |
|---|---|
| `name` | Used in logs. |
| `id` | Tuya device id (`gwId`). |
| `ip` | Optional. Leave empty to discover the device by broadcast; it is re‑discovered automatically if the address changes. |
| `local_key` | 16‑character local key. |
| `accessories` | HomeKit accessories built from this device (below). |
| `enforce` | Rules that keep DPs at a value (below). |

## Accessories

Accessory order matters: HomeKit identifies accessories by position. Append new
accessories at the end to keep existing automations working.

### `switch`

| Key | Description |
|---|---|
| `name` | Name in the Home app. |
| `state` | The switch shows **on** when *all* of these DPs have these values. |
| `on` | DPs written when the switch is turned on. |
| `off` | DPs written when the switch is turned off. |
| `on_then` / `off_then` | Optional second write after `on` / `off`. Some devices ignore certain DPs in the same command – e.g. a heater that ignores its power level while it is off, so `"on": {"1": true}, "on_then": {"4": "low"}` is needed to start at low power. |

Writing several DPs at once lets one switch express a combined action, e.g. "turn on
*and* set high power".

### `temperature`

| Key | Description |
|---|---|
| `name` | Name in the Home app. |
| `dp` | DP that holds the temperature. |
| `scale` | Optional divisor, e.g. `10` if the device reports 215 for 21.5 °C. |

## Enforce rules

```json
"enforce": [ { "when": { "1": true }, "set": { "2": 50 } } ]
```

On every poll, if all `when` DPs match and the `set` DPs differ, the bridge writes
`set`. Useful to pin a heater's built‑in thermostat to its maximum so that an external
sensor (e.g. a HomePod) is in charge, or to undo changes made on the device panel.

## Values

DP values are JSON values: `true`/`false`, numbers and strings. They are compared by
their textual form, so `50` matches `50` and `"low"` matches `"low"`.
