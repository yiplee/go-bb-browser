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
  `BB_BROWSER_DEBUGGER_URL` and `BB_BROWSER_LISTEN`):

  ```ini
  [Service]
  Environment=BB_BROWSER_DEBUGGER_URL=http://127.0.0.1:9222
  Environment=BB_BROWSER_LISTEN=127.0.0.1:8765
  ```

## Uninstall

```bash
systemctl --user disable --now bb-daemon.service
rm "$HOME/.config/systemd/user/bb-daemon.service"
systemctl --user daemon-reload
```
