# Local web UI

For people without Apple devices, the bridge can serve a small, password‑protected web
page with on/off buttons for every `switch` accessory and readings for `temperature`
accessories. It works in any phone or desktop browser on the same network and stays in
sync with the Home app (both talk to the same bridge).

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

Open `http://<bridge-ip>:8080`. On OpenWrt you can add a friendly name with
`LAN_ALIASES="heating"` in `build.env` and open `http://heating:8080`.

## Security

- Plain HTTP, meant for the **local network only**. Do not port‑forward it to the
  internet; use a VPN (e.g. WireGuard) for remote access instead.
- Logins create an HTTP‑only, `SameSite=Strict` session cookie valid for 30 days
  (sessions are kept in memory and reset when the bridge restarts).
- Failed logins are logged with the client address and delayed by one second.
- State‑changing requests from other sites are rejected (Origin check).
- Every switch change is logged with the client address.
