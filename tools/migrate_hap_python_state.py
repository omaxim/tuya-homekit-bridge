#!/usr/bin/env python3
"""Migrate a HAP-python `accessory.state` file to this bridge's storage directory.

Keeps the existing HomeKit pairing, so the Home app does not need to re-pair.
The new bridge must expose the same number and order of accessories.

    python3 tools/migrate_hap_python_state.py accessory.state db/
"""
import base64
import json
import os
import sys

if len(sys.argv) != 3:
    sys.exit(__doc__)
src, dst = sys.argv[1], sys.argv[2]
s = json.load(open(src))
os.makedirs(dst, exist_ok=True)

b64 = lambda b: base64.b64encode(b).decode()
seed, pub = bytes.fromhex(s["private_key"]), bytes.fromhex(s["public_key"])


def write(name, data):
    path = os.path.join(dst, name)
    with open(path, "w") as f:
        f.write(data)
    os.chmod(path, 0o600)


write("keypair", json.dumps({"Public": b64(pub), "Private": b64(seed + pub)}))
write("uuid", s["mac"])
write("version", str(s.get("config_version", 1) + 1))
for cid, pk in s["paired_clients"].items():
    name = bytes.fromhex(s["client_uuid_to_bytes"][cid]).decode()
    perm = s["client_properties"].get(cid, {}).get("permissions", 0)
    write(name.encode().hex() + ".pairing",
          json.dumps({"Name": name, "PublicKey": b64(bytes.fromhex(pk)), "Permission": perm}))
print(f"Migrated {len(s['paired_clients'])} pairing(s) to {dst}")
