# Finding out what each data point does

Tuya devices expose their state as numbered **data points** (DPs). The meaning of each
DP depends on the product. To map them:

1. Put the device id and local key into a minimal config (no accessories needed):

   ```json
   { "pin": "12345678", "devices": [ { "name": "Test", "id": "…", "local_key": "…", "accessories": [] } ] }
   ```

2. Read the current state:

   ```bash
   tuya-homekit-bridge -config test.json -probe
   # Test: map[1:true 2:40 3:18 4:low 7:true 10:true 19:cancel] (error: <nil>)
   ```

3. Change one thing on the device itself or in the Smart Life app (power, target
   temperature, mode, …) and probe again. The DP that changed is the one you touched.

4. `devices.json` from `tools/fetch_keys.py` also lists the DP *codes* and value ranges
   the cloud knows about (e.g. `lock`, `light`, `countdown_set`). Cloud codes do not
   always cover every local DP, so step 3 is the reliable method.

## Example: Klarstein Bansin Smart convector heater

| DP | Meaning | Values |
|---|---|---|
| 1 | Power | `true` / `false` |
| 2 | Target temperature (°C) | integer, max 50 |
| 3 | Current temperature at the heater (°C) | integer, read‑only |
| 4 | Power level | `"low"` / `"high"` |
| 7 | Child lock | `true` / `false` |
| 10 | Display light | `true` / `false` |
| 19 | Countdown timer | `"cancel"`, `"1h"` … `"6h"` |

Please open a pull request adding a table for your device to help others.
