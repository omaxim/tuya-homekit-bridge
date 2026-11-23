# Reading the HomePod temperature sensor directly

The HomePod mini / HomePod (2nd gen) temperature sensor is exposed inside your Home as a
regular HomeKit (HAP) accessory, advertised on the LAN as `_hap._tcp`
("HomePodSensor …"). HomeKit only lets controllers that are paired with it talk to it,
so third‑party software cannot normally read it – and Home automations that read it on
the hub are unreliable (on HomePod software 27.x they return an empty value or an
unrelated one).

This page shows how to give the bridge **its own, limited pairing** with that accessory,
so it can read the temperature every minute without any automation:

```
HomePod sensor accessory ◄──(HAP, encrypted, user permission)── tuya-homekit-bridge ──► heater
```

You do this once. It needs your Home's existing HomeKit key, which only exists on your
Apple devices, so it is taken – once – from an **encrypted local iPhone backup**.

> **Security notes**
>
> - The Home key from the backup is an *admin* key for your whole Home. It is used for
>   a single request (adding the new pairing) and should be deleted right afterwards.
>   Never store it on the router, in a repository or in cloud storage.
> - The bridge only keeps the **new** key, which has *user* permission on the sensor
>   accessory alone: it can read the sensor, it cannot add or remove pairings and is
>   not valid for any other accessory. Treat it like the other secrets in the config.
> - `tools/hapkeys` decrypts the backup in memory, keeps only the
>   `com.apple.hap.pairing` keychain items and writes nothing else. It links no network
>   code.

## 1. Make an encrypted backup

Connect the iPhone to a Mac, open **Finder → iPhone → General**, choose *Back up all of
the data on your iPhone to this Mac*, tick **Encrypt local backup**, set a password and
click **Back Up Now**. (The keychain – and with it the HomeKit keys – is only included
in encrypted backups.) A Windows PC with Apple Devices / iTunes works too.

## 2. Extract the HomeKit keys

```sh
cd tools/hapkeys
go build -o hapkeys .
codesign -f -s - hapkeys     # macOS: re-sign, or the system may kill the binary
./hapkeys -out ~/hap-keys.json
```

Run it in a terminal that has **Full Disk Access** (System Settings → Privacy &
Security), as the backups folder is protected. It asks for the backup password and
prints the HomeKit pairing identities and paired accessories it found, by ID only.
Write the output outside cloud‑synced folders.

`tools/hapkeys` is based on [irestore](https://github.com/dunhamsteve/ios) by Steve
Dunham (MIT); the keychain item parsing was updated for current iOS versions.

## 3. Find the sensor and pair the bridge

The accessory ID is the `id` value of the sensor's mDNS record:

```sh
dns-sd -B _hap._tcp            # look for "HomePodSensor …"
dns-sd -L "HomePodSensor 12345 ABCD" _hap._tcp local    # shows id=AA:BB:CC:DD:EE:FF
```

Then:

```sh
go run ./cmd/hap-pair -keys ~/hap-keys.json -id AA:BB:CC:DD:EE:FF -out sensor.json
rm ~/hap-keys.json
```

`hap-pair` finds the accessory via mDNS, verifies with whichever of your identities
the accessory accepts, reads the temperature, adds a new controller with *user*
permission, checks that the new key works and writes it to `sensor.json`. Finally
delete the iPhone backup (Finder → Manage Backups…).

## 4. Configure the bridge

Put the content of `sensor.json` into the thermostat config:

```json
"thermostat": {
  "device": 0,
  "homekit_sensor": {
    "accessory_id": "AA:BB:CC:DD:EE:FF",
    "accessory_ltpk": "<64 hex>",
    "controller_id": "<UUID>",
    "controller_seed": "<64 hex, secret>"
  },
  "off":   { "set": { "1": false } },
  "heat":  { "set": { "2": 50, "1": true }, "then": { "4": "low" } },
  "boost": { "set": { "2": 50, "1": true }, "then": { "4": "high" } }
}
```

| Key | Default | Description |
|---|---|---|
| `accessory_id`, `accessory_ltpk` | – | The sensor accessory and its public key (from `hap-pair`). |
| `controller_id`, `controller_seed` | – | The bridge's own pairing (secret). |
| `address` | mDNS | Fixed `host:port`. Normally leave empty: the HomePod picks a new port after a restart and the bridge looks it up again. |
| `interval` | 1m | How often the temperature is read. |

With a `homekit_sensor` the `token` (HTTP temperature push) becomes optional, and the
`trigger` motion sensor is not needed. The log shows
`homekit sensor: connected …` and the temperature whenever it changes.

## Undoing it

The pairing stays valid until the sensor accessory is reset (removing the HomePod
from the Home, or resetting it). To revoke it earlier, remove the HomePod from the
Home and add it again; then repeat this page if you still want the bridge to read it.
