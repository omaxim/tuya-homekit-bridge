# Room thermostat with a HomePod temperature sensor

HomePod mini and HomePod (2nd gen) have a temperature sensor. HomeKit does not let
third‑party accessories read it, but Home app **automations** can react to it. With two
switches – *Heater* (on/off) and *Heater boost* (high/low power) – you get staged
control that heats fast when the room is cold and gently near the set point.

## Bridge config

Use the [Klarstein example](../examples/heater-klarstein-bansin-smart.json): the
`enforce` rule pins the heater's own thermostat (DP 2) to its maximum while it is on, so
it never switches itself off and the HomePod decides.

## Automations

In the Home app: **Automation → + → A Sensor Detects Something →** the room's
temperature.

| Trigger | Action |
|---|---|
| drops below 20.5 °C | Heater **on** |
| drops below 19.5 °C | Heater boost **on** |
| rises above 20.0 °C | Heater boost **off** |
| rises above 21.0 °C | Heater **off** |

Result: below 19.5 °C full power, between 19.5 and 21 °C low power, above 21 °C off.
The gaps between the thresholds (hysteresis) prevent rapid switching.

Notes:

- Automations fire only when a threshold is **crossed**. Right after creating them,
  switch the heater on manually once if the room is already cold.
- To change the target temperature, edit the four thresholds. Add time conditions
  (e.g. 07:00–22:00) and a second set with lower thresholds for night.
- Automations run on the home hub (HomePod/Apple TV), so they work without your phone.
- If the bridge or Wi‑Fi goes down while the heater is on, it keeps heating until its own
  thermostat limit. Rely on the heater's built‑in overheat protection.
