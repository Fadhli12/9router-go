### feat(cli): service autostart management

`9router-go service install` registers the gateway to start on boot using
whatever init the machine actually runs:

- **systemd** — always the per-user unit (`systemd --user`), so install stays
  unprivileged and can never touch a root-owned unit. Every
  install/uninstall/status/control call goes through one `systemdScope` helper,
  which keeps `--user` from being dropped and the command from landing on the
  system bus (root prompt, user unit left enabled). There is no system-unit
  scope or `--system` flag.
- **launchd** (macOS), **runit** (root, or `~/service` under a user `runsvdir`),
  and **cron `@reboot`** plus an XDG autostart desktop entry, all tagged with a
  `# 9router-go-autostart` marker so install is idempotent and uninstall never
  touches the user's own entries.
- **OpenRC** is a system service and needs root: it has no per-user scope, so
  `service install` refuses without it rather than silently installing a
  different backend (run with `sudo`, or use `--backend cron`).
- **Windows** — a scheduled task created with `schtasks` at logon, run as the
  installing user rather than `SYSTEM` (a SYSTEM task reads a different
  %APPDATA% data dir). Creating the task requires an Administrator console.

Companions: `service start|stop|restart` drive the supervised service (falling
back to the pid-file daemon for cron, which has no supervisor), `status` reports
the entry in effect, and `uninstall` stops/disables before deleting and reports
failure when the file survives. `install` warns when the `start` daemon is still
holding the port, and an unmatched positional argument now fails fast with
`unknown command` instead of booting a second gateway.

The home directory is read through a package seam (`userHomeDir`) rather than
`HOME`, because `os.UserHomeDir` reads `%USERPROFILE%` on Windows; tests point
every user-scoped path (systemd unit, launchd plist, autostart entry, runit
`~/service`) at a temp home instead of the runner's real one. Windows task
commands are quoted for `cmd.exe` rather than the POSIX shell, so a path under
`C:\Program Files\...` still fires.

Verified with `go test ./...` (all per-OS backends are exercised from one runner
through the seams) plus `make build`.
