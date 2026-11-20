# Local web UI

For people without Apple devices, the bridge can serve a small, password‑protected web
page: a thermostat dial (drag or −/+, Off/Heat) when a [thermostat](homepod-thermostat.md)
is configured, and tiles for every `switch` and `temperature` accessory. It works in any
phone or desktop browser on the same network, follows the system light/dark theme and
stays in sync with the Home app instantly (both talk to the same bridge; the page
receives changes via Server‑Sent Events and falls back to polling).

```json
"web": {
  "listen": ":8080",
  "password": "choose-a-shared-password",
  "title": "Studio heating",
  "lang": "en"
}
```

| Key | Description |
|---|---|
| `listen` | Address to listen on, e.g. `":8080"` (all interfaces) or `"192.168.1.1:8080"`. |
| `password` | Shared password. Leaving it empty disables the login – only do that on a trusted network. |
| `title` | Page title, defaults to the bridge `name`. |
| `lang` | `en` (default) or `cs`. Translations are welcome. |

Open `http://<bridge-ip>:8080`.

### Friendly URL on OpenWrt (no port number)

Give the router a second LAN address for the web UI, keep LuCI on the first one, and
add a host name – put this into a file referenced by `EXTRA_UCI_DEFAULTS` in
`build.env` and set `"listen": "192.168.1.2:80"`:

```sh
uci set network.lan_web=interface
uci set network.lan_web.proto='static'
uci set network.lan_web.device='br-lan'
uci set network.lan_web.ipaddr='192.168.1.2'
uci set network.lan_web.netmask='255.255.255.0'
uci add_list firewall.@zone[0].network='lan_web'
uci -q delete uhttpd.main.listen_http
uci add_list uhttpd.main.listen_http='192.168.1.1:80'
uci add_list uhttpd.main.listen_http='[::]:80'
# single-label names need a hosts entry
uci add dhcp domain >/dev/null
uci set dhcp.@domain[-1].name='heating'
uci set dhcp.@domain[-1].ip='192.168.1.2'
```

Then `http://heating` opens the heating page and the router's own UI stays on
`http://192.168.1.1`.

With a [thermostat](homepod-thermostat.md) configured, the page also shows the room
temperature, the target with −/+ buttons and an on/off button for the thermostat, and
serves `GET|POST /api/temperature?token=…&value=…` for automations (token instead of a
login).

## Security

- Plain HTTP, meant for the **local network only**. Do not port‑forward it to the
  internet; use a VPN (e.g. WireGuard) for remote access instead.
- Logins create an HTTP‑only, `SameSite=Strict` session cookie valid for 30 days
  (sessions are kept in memory and reset when the bridge restarts).
- Failed logins are logged with the client address and delayed by one second.
- State‑changing requests from other sites are rejected (Origin check).
- Every switch change is logged with the client address.
