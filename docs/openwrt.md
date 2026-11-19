# Running on an OpenWrt router

A router is always on and always on the right LAN, which makes it an ideal home for the
bridge. This recipe builds a complete OpenWrt image that contains:

- your network settings (LAN address, WAN incl. PPPoE/VLAN, Wi‑Fi),
- the bridge configuration and, optionally, an existing HomeKit pairing,
- WireGuard for remote administration,
- SSH key login (password login disabled).

Everything is part of the image defaults, so pressing the router's reset button brings
back **this** configuration instead of a blank OpenWrt.

Optional extras (see `build.example.env`): a second Wi‑Fi network on every radio
(`WIFI2_*`, e.g. the SSID and key printed on the router's label so the label stays
valid), a root/web‑UI password (`ROOT_PASSWORD`), DNS aliases such as the vendor's
`tplinkwifi.net`, and the LuCI web interface (`luci-light`, fits on 8 MB devices when the
bridge runs from RAM).

## Small flash? Run the bridge from RAM

The bridge binary is ~10 MB (≈2.4 MB compressed). Routers with 8 MB of flash (e.g.
TP‑Link Archer C6 v2 EU) have only a few hundred KB free after OpenWrt itself, so the
image does **not** contain the binary. Instead, `/usr/sbin/tuya-homekit-bridge-run`:

1. syncs the clock (routers have no RTC; TLS fails until it is right), then downloads
   the pinned release asset into `/tmp` (RAM), trying each URL in `BRIDGE_URL` in turn
   (GitHub release, then the `bin` branch via raw.githubusercontent.com and jsDelivr),
2. verifies its SHA‑256 against the value baked into the image,
3. starts it, and retries every 30 s until the internet and the clock are up.

Trade‑off: after a power cut the bridge starts once the router is online (usually within
a minute). If the router has enough flash, put the binary at `/usr/bin/tuya-homekit-bridge`
(e.g. via the image's `files/` tree) and it is used directly, no download needed.

Memory: the UPX‑compressed binary needs ~2.5 MB in `/tmp` plus ~15 MB while running;
64 MB of RAM is enough.

## Build

Requirements: Docker, the bridge config, and the OpenWrt target/profile of your router
(see the [Table of Hardware](https://openwrt.org/toh/start) and
`https://downloads.openwrt.org/releases/<release>/targets/<target>/profiles.json`).

```bash
cd openwrt
cp build.example.env build.env    # git-ignored
$EDITOR build.env
./build.sh
```

Bridge config on the router should use:

```json
"storage": "/etc/tuya-homekit-bridge/db",
"interfaces": ["br-lan"]
```

`BRIDGE_SHA256` must match the asset in `BRIDGE_URL`; every release publishes a
`sha256sums.txt`.

### WAN VLAN on boards with a built-in switch

On swconfig boards (e.g. Archer C6 v2) the WAN port is itself a switch VLAN
(`eth0.2`). With `WAN_VLAN` set, the build re-tags that switch VLAN (WAN port and CPU
port tagged) and uses `eth0.<WAN_VLAN>` for WAN, instead of stacking a second VLAN tag.
On DSA boards an 802.1Q device on the WAN port is created.

### Example: O2 / CETIN line in Czechia

CETIN VDSL/fibre lines need PPPoE on VLAN 848 with the generic credentials
`CETIN` / `CETIN` (the CETIN modem stays in bridge mode):

```bash
WAN_PROTO=pppoe
WAN_VLAN=848
PPPOE_USER=CETIN
PPPOE_PASS=CETIN
```

## Flash

1. **Note your current settings first** (WAN type, VLAN, PPPoE credentials, Wi‑Fi). A
   wrong WAN setting is the most common reason for "no internet" afterwards.
2. From the vendor firmware: upload `*-squashfs-factory.bin` via its firmware upgrade
   page. From OpenWrt: `sysupgrade -n *-squashfs-sysupgrade.bin`.
3. Wait ~3 minutes. Connect to your Wi‑Fi (same SSID/key as configured) and
   `ssh root@<LAN_IP>`.
4. Check the bridge: `logread -e tuya-homekit-bridge`.

Some vendor firmware versions reject OpenWrt images; check the device page on
openwrt.org for the accepted versions and the TFTP recovery procedure.

## Remote administration with WireGuard

The image includes `kmod-wireguard` and `wireguard-tools`. Whether the router can be a
WireGuard **server** depends on the upstream connection:

- Public IPv4 on the router's WAN → create a `wg0` interface listening on a UDP port and
  open that port in the firewall.
- Behind NAT/CGNAT (common with PPPoE on some ISPs or with an upstream modem‑router) →
  make the router a WireGuard **client** of a server that is reachable (a VPS, a home
  router with a public IP, …) and enable `persistent_keepalive`.

See the [OpenWrt WireGuard guide](https://openwrt.org/docs/guide-user/services/vpn/wireguard/start).

## Updating the bridge

Publish/choose a new release, update `URL` and `SHA256` in
`/etc/tuya-homekit-bridge/loader.conf`, then:

```bash
rm -f /tmp/tuya-homekit-bridge && /etc/init.d/tuya-homekit-bridge restart
```

(also update `build.env` so a factory reset uses the new version).
