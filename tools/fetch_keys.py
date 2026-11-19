#!/usr/bin/env python3
"""Fetch Tuya device ids and local keys via the Smart Life QR login.

Uses the same QR login flow as Home Assistant's Tuya integration
(tuya-device-sharing-sdk), so no Tuya developer account is needed.

    pip install tuya-device-sharing-sdk "qrcode[pil]"
    python3 tools/fetch_keys.py <Smart Life user code>

Writes devices.json (contains local keys - keep it private, never commit it).
"""
import json
import subprocess
import sys
import time

import qrcode
from tuya_sharing import LoginControl, Manager

# Public client id/schema of the Home Assistant Tuya integration.
CLIENT_ID = "HA_3y9q4ak7g4ephrvke"
SCHEMA = "haauthorize"


def plain(obj):
    """Convert SDK objects to JSON-serialisable data."""
    if isinstance(obj, dict):
        return {k: plain(v) for k, v in obj.items()}
    if isinstance(obj, (list, tuple)):
        return [plain(v) for v in obj]
    if hasattr(obj, "__dict__"):
        return plain(vars(obj))
    try:
        json.dumps(obj)
        return obj
    except TypeError:
        return str(obj)


def main(user_code: str) -> None:
    lc = LoginControl()
    resp = lc.qr_code(CLIENT_ID, SCHEMA, user_code)
    if not resp.get("success"):
        sys.exit(f"QR request failed: {resp.get('msg') or resp}")
    token = resp["result"]["qrcode"]
    qrcode.make(f"tuyaSmart--qrLogin?token={token}").save("qr.png")
    if sys.platform == "darwin":
        subprocess.run(["open", "qr.png"], check=False)
    print("Scan qr.png in Smart Life (Me -> scan icon, top right) and confirm the login...", flush=True)

    deadline = time.time() + 300
    while True:
        ok, info = lc.login_result(token, CLIENT_ID, user_code)
        if ok:
            break
        if time.time() > deadline:
            sys.exit("Timed out after 5 minutes.")
        time.sleep(3)

    token_info = {k: info[k] for k in ("t", "uid", "expire_time", "access_token", "refresh_token")}
    mgr = Manager(CLIENT_ID, user_code, info["terminal_id"], info["endpoint"], token_info)
    mgr.update_device_cache()

    devices = []
    for d in mgr.device_map.values():
        devices.append({
            "name": d.name,
            "id": d.id,
            "local_key": d.local_key,
            "category": d.category,
            "product_id": d.product_id,
            "online": d.online,
            "status": plain(d.status),
            "functions": plain(d.function),
            "status_range": plain(d.status_range),
        })
    with open("devices.json", "w") as f:
        json.dump(devices, f, indent=2, ensure_ascii=False)
    print(f"\nWrote devices.json with {len(devices)} device(s):")
    for d in devices:
        print(f"  {d['name']!r}: id={d['id']} category={d['category']} key={'yes' if d['local_key'] else 'no'}")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit(__doc__)
    main(sys.argv[1])
