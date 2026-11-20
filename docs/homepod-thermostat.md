# Room thermostat with a HomePod temperature sensor

HomePod mini and HomePod (2nd gen) have a temperature sensor, but HomeKit does not let
third‑party software read it. There are two ways to use it:

1. **Thermostat mode (recommended)** – a Home automation running on the HomePod
   periodically sends the temperature to the bridge, and the bridge regulates the
   device itself. You get a real thermostat with a slider in the Home app and in the
   web UI, for everyone.
2. **Threshold automations** – no bridge changes; Home automations switch the device on
   and off at fixed thresholds (see the end of this page).

## 1. Thermostat mode

```
HomePod sensor ──(Home automation, every 5 min, runs on the HomePod)──► GET /api/temperature
                                                                         │
                         tuya-homekit-bridge: off / heat / boost  ◄──────┘
                                   │
                          Tuya heater (LAN)
```

### Bridge configuration

Requires the [web UI](web-ui.md) (the temperature endpoint lives there).

```json
"thermostat": {
  "name": "Thermostat",
  "device": 0,
  "default_target": 21,
  "min_target": 10,
  "max_target": 28,
  "hysteresis": 0.3,
  "boost_below": 1.0,
  "max_age": "20m",
  "token": "<long random string>",
  "off":   { "set": { "1": false } },
  "heat":  { "set": { "2": 50, "1": true }, "then": { "4": "low" } },
  "boost": { "set": { "2": 50, "1": true }, "then": { "4": "high" } },
  "trigger": { "name": "Thermostat trigger", "interval": "5m" }
}
```

| Key | Default | Description |
|---|---|---|
| `device` | – | Index of the device in `devices` that is regulated. |
| `default_target` | 21 | Target used until someone changes it (stored in `storage`, so it survives bridge restarts; on a stateless router it resets on reboot). |
| `min_target` / `max_target` | 10 / 28 | Range of the slider. |
| `hysteresis` | 0.3 | °C above/below the target before switching, prevents rapid cycling. |
| `boost_below` | 1.0 | Use the `boost` stage when the room is this far below the target. |
| `max_age` | 20m | **Failsafe:** if no temperature arrived for this long, the device is switched off. |
| `token` | – | Secret for `/api/temperature` (≥ 12 characters). |
| `off` / `heat` / `boost` | – | DPs written for each stage (`then` = second write, see `on_then`). `boost` is optional. |
| `trigger` | – | Adds a motion sensor that "detects motion" every `interval`, used to start the Home automation periodically. |

Regulation (target 21 °C, defaults): below 20.0 °C → boost, below 20.7 °C → heat,
above 21.3 °C → off; in between the current stage is kept. The thermostat's
**Off** mode switches the device off once and then leaves it alone, so manual switches
keep working; in **Heat** mode the bridge corrects manual changes on the next update.

The thermostat appears in the Home app with a target slider, the room temperature and
Off/Heat. The web UI shows the same with −/+ buttons.

### Home automation (runs on the HomePod)

1. Home app → **Automation → +** → **A Sensor Detects Something** → pick the
   *Thermostat trigger* motion sensor → **Detects Motion**.
2. Choose any action, then scroll down and tap **Convert to Shortcut**.
3. Delete the placeholder action and add:
   1. **Get the state of** → *[HomePod temperature sensor]* → **Current Temperature**
   2. **Get Contents of URL** →
      `http://<bridge-ip>/api/temperature?token=<token>&value=` followed by the
      **Current Temperature** variable (tap the end of the URL and insert the variable).
      Method **GET**.
4. Done. The HomePod now sends the temperature every 5 minutes.

Tips:

- Comma decimals ("21,5"), the "°C" suffix and °F are all accepted.
- Check it works: the web UI shows "Room temperature from … min ago".
- Delete any old threshold automations so they do not fight the thermostat.
- Optional extra safety: a separate automation "if the HomePod temperature rises above
  25 °C → set the thermostat to Off".

## 2. Threshold automations (no thermostat)

With two switches – *Heater* (on/off) and *Heater boost* (high/low power) – Home app
automations on the room temperature give staged control:

| Trigger | Action |
|---|---|
| drops below 20.5 °C | Heater **on** |
| drops below 19.5 °C | Heater boost **on** |
| rises above 20.0 °C | Heater boost **off** |
| rises above 21.0 °C | Heater **off** |

Automations fire only when a threshold is **crossed**; to change the target you edit
the thresholds.
