# systemd --user unit for `bb-daemon`

Run `bb-daemon` as a per-user service that attaches to an already-running Chrome
with a DevTools endpoint on `127.0.0.1:9222`. The daemon never starts Chrome
itself — start Chrome separately (e.g. `google-chrome --remote-debugging-port=9222`).

## Install

```bash
# 1) Build and install the binary to ~/.local/bin (must be on PATH for shell use,
#    but the unit references the absolute path so PATH isn't required for systemd).
go build -o "$HOME/.local/bin/bb-daemon" ./cmd/bb-daemon

# 2) Make sure the writable state dir referenced by ReadWritePaths= exists.
mkdir -p "$HOME/.local/state/bb-daemon"

# 3) Drop the unit into the user systemd directory.
mkdir -p "$HOME/.config/systemd/user"
cp deploy/systemd/bb-daemon.service "$HOME/.config/systemd/user/bb-daemon.service"

# 4) Reload, enable, start.
systemctl --user daemon-reload
systemctl --user enable --now bb-daemon.service
```

## Verify

```bash
systemctl --user status bb-daemon.service
journalctl --user -u bb-daemon.service -f
```

## Run on boot (no login required)

By default `systemd --user` only runs while you have an active session. To keep
it running across reboots without logging in, enable lingering for your user:

```bash
sudo loginctl enable-linger "$USER"
```

## Customize

- **Debugger endpoint:** edit `ExecStart=` in the unit, or use a drop-in:

  ```bash
  systemctl --user edit bb-daemon.service
  ```

  ```ini
  [Service]
  ExecStart=
  ExecStart=%h/.local/bin/bb-daemon --debugger-url http://127.0.0.1:9333
  ```

- **Environment variables** (alternative to flags — `bb-daemon` reads
  `BB_BROWSER_DEBUGGER_URL`, `BB_BROWSER_LISTEN`, `BB_BROWSER_API_TOKEN`,
  `BB_BROWSER_API_TOKEN_FILE`, `BB_BROWSER_API_TOKEN_ALLOW_LOOPBACK`, `BB_BROWSER_TAB_IDLE_TIMEOUT`,
  `BB_BROWSER_STATE_DIR`, `BB_BROWSER_CDP_WATCHDOG_INTERVAL`,
  `BB_BROWSER_CDP_WATCHDOG_TIMEOUT`, `BB_BROWSER_CDP_WATCHDOG_FAILURES`,
  `BB_BROWSER_OBSERVER_IDLE_TIMEOUT`, `BB_BROWSER_LOG_LEVEL`, and
  `BB_BROWSER_LOG_FORMAT`):

  ```ini
  [Service]
  Environment=BB_BROWSER_DEBUGGER_URL=http://127.0.0.1:9222
  Environment=BB_BROWSER_LISTEN=127.0.0.1:8765
  Environment="BB_BROWSER_API_TOKEN=<caller-a-token>,<caller-b-token>"
  Environment=BB_BROWSER_API_TOKEN_ALLOW_LOOPBACK=false
  Environment=BB_BROWSER_TAB_IDLE_TIMEOUT=5m
  Environment=BB_BROWSER_STATE_DIR=%h/.local/state/bb-daemon
  Environment=BB_BROWSER_CDP_WATCHDOG_INTERVAL=5s
  Environment=BB_BROWSER_CDP_WATCHDOG_TIMEOUT=2s
  Environment=BB_BROWSER_CDP_WATCHDOG_FAILURES=3
  Environment=BB_BROWSER_OBSERVER_IDLE_TIMEOUT=5m
  Environment=BB_BROWSER_LOG_LEVEL=info
  Environment=BB_BROWSER_LOG_FORMAT=json
  ```

  Replace token placeholders before use. Alternatively set `Environment=BB_BROWSER_API_TOKEN_FILE=/path/to/api-tokens`
  to read one token per line (blank and `#` comment lines ignored). Token sources merge after trimming and deduplication;
  an empty whitelist disables auth. Changes require restart. `POST /v1` requires a matching Bearer token when configured unless the loopback exemption below applies;
  failures return standard HTTP 401 with plain text `unauthorized\n` and never dispatch or write the RPC log.
  `/live`, `/ready`, `/health` stay public for probes. Without tokens, non-loopback listening only warns.

  `--api-token-allow-loopback` / `BB_BROWSER_API_TOKEN_ALLOW_LOOPBACK` defaults to false; flags override the
  environment. Boolean values follow `strconv.ParseBool` (`true/false`, `1/0`, `t/f` and supported case variants);
  invalid or empty environment values fall back to false. With configured tokens, it exempts only real TCP
  loopback peers with no proxy headers, even if Authorization contains a wrong token. Without tokens it has
  no effect and logs an info at startup; with tokens it logs one warn. Health routes stay public.

  Direct local callers and Tailscale **HTTP forwarding** (Serve/Funnel default mode, proxy headers preserved)
  may enable it; forwarded requests still require a token. **Do not enable** with Tailscale **TCP forwarding**
  (`tailscale serve --tcp` / `--tls-terminated-tcp`), local reverse proxies that may strip headers, Docker port
  mappings, cloudflared tunnels, or uncertain proxy paths. Header detection is only a fallback, not the primary
  defense. See the [repository README](../../README.md) for the full header list and Tailscale evidence.

  Tab-related RPC log lines live in `rpc.jsonl` under `{StateDir}`, so idle cleanup can be rebuilt after daemon
  restarts by intersecting live CDP tabs with the log's per-tab activity. The log auto-rotates past ~8 MiB into
  `rpc.jsonl.1`… backups (3 kept). The unit's `ReadWritePaths=` already
  allows `%h/.local/state/bb-daemon`; you may also set `StateDirectory=bb-daemon`
  under `[Service]` for systemd-managed state layout.

`Restart=on-failure` restarts the daemon after the CDP supervisor confirms a broken
session. External process monitors should query `GET /live` (it never calls CDP);
reverse proxies and schedulers should use `GET /ready` before sending traffic.

## Uninstall

```bash
systemctl --user disable --now bb-daemon.service
rm "$HOME/.config/systemd/user/bb-daemon.service"
systemctl --user daemon-reload
```
