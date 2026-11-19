# Getting the device id and local key

Tuya devices encrypt their LAN traffic with a per‑device **local key**. It is generated
when the device is paired with an app and is only known to the Tuya cloud and the
device. You need it once; after that the bridge works without the cloud.

> Re‑pairing the device (removing it from the app and adding it again) generates a new
> local key. Fetch the key **after** the device is paired in its final app.

## Option A – Smart Life QR login (recommended, no developer account)

This uses the same QR login flow as the official Tuya integration in Home Assistant.

1. Pair the device in the **Smart Life** (or **Tuya Smart**) app.
   Devices from white‑label apps (e.g. a manufacturer's own app) usually can be moved to
   Smart Life: remove them from the original app, put them into pairing mode and add
   them in Smart Life.
2. In Smart Life open **Me → ⚙ Settings → Account and Security → User Code** and note
   the code.
3. Run:

   ```bash
   python3 -m venv .venv && .venv/bin/pip install tuya-device-sharing-sdk "qrcode[pil]"
   .venv/bin/python tools/fetch_keys.py <user code>
   ```

4. A QR code opens (`qr.png`). Scan it in Smart Life (**Me →** scan icon, top right) and
   confirm the login.
5. The script prints your devices and writes `devices.json` with `id`, `local_key`,
   `product_id` and the cloud's data point definitions. **Keep this file private.**

## Option B – Tuya IoT developer platform

Create a cloud project at <https://iot.tuya.com>, link your Smart Life account and use
`python -m tinytuya wizard` from [tinytuya](https://github.com/jasonacox/tinytuya). See
the tinytuya README for the current steps.

## Finding the device on the LAN

```bash
tuya-homekit-bridge -discover 15s
```

prints the id, IP address and protocol version of every protocol 3.5 device it can
see. Only devices that report `version=3.5` are supported.
