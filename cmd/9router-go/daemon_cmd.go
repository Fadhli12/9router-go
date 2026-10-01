package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/urfave/cli/v2"

	"9router/proxy/internal/daemon"
)

// daemonURL is the address the background daemon is expected to answer on.
// A custom HOST/PORT only takes effect when the operator set it in the
// environment; the default listener is what the flag-free start path uses.
func daemonURL() string {
	if host := strings.TrimSpace(os.Getenv("HOST")); host != "" {
		if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
			return "http://" + joinHostPort(host, port)
		}
	}
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return "http://127.0.0.1:" + port
	}
	return "http://127.0.0.1:20130"
}

// joinHostPort wraps bare IPv6 literals in brackets.
func joinHostPort(host, port string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// startDetached runs the server as a background daemon and returns, leaving
// the terminal free.
func startDetached(cCtx *cli.Context) error {
	url := daemonURL()
	res, err := daemon.Start(url, forwardArgs(cCtx))
	if err == daemon.ErrAlreadyRunning {
		fmt.Printf("9router-go already running (pid %d) → %s\n", res.PID, res.URL)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("9router-go started in background (pid %d)\n", res.PID)
	fmt.Printf("  Dashboard: %s\n", res.URL)
	fmt.Printf("  Log:       %s\n", daemon.LogPath())
	fmt.Println("  Stop with: 9router-go stop")
	return nil
}

// stopDetached stops the background daemon.
func stopDetached(_ *cli.Context) error {
	res, err := daemon.Stop()
	if err != nil {
		return err
	}
	if !res.Stopped {
		fmt.Println("9router-go is not running in background.")
		return nil
	}
	fmt.Printf("9router-go stopped (pid %d).\n", res.PID)
	return nil
}

// restartDetached replaces a running daemon with a fresh one.
func restartDetached(cCtx *cli.Context) error {
	url := daemonURL()
	res, err := daemon.Restart(url, forwardArgs(cCtx))
	if err != nil {
		return err
	}
	fmt.Printf("9router-go restarted in background (pid %d)\n", res.PID)
	fmt.Printf("  Dashboard: %s\n", res.URL)
	fmt.Printf("  Log:       %s\n", daemon.LogPath())
	return nil
}

// statusDetached reports whether a background daemon is live.
func statusDetached(_ *cli.Context) error {
	pid := daemon.RunningPID()
	if pid == 0 {
		fmt.Println("9router-go is not running in background.")
		return nil
	}
	fmt.Printf("9router-go is running in background (pid %d)\n", pid)
	fmt.Printf("  Dashboard: %s\n", daemonURL())
	fmt.Printf("  Log:       %s\n", daemon.LogPath())
	return nil
}

// logsDetached prints the tail of the background run log.
func logsDetached(cCtx *cli.Context) error {
	lines := 40
	if cCtx.IsSet("lines") {
		if v := cCtx.Int("lines"); v > 0 {
			lines = v
		}
	}
	tail := daemon.LogTail(lines)
	if tail == "" {
		fmt.Printf("No background log yet at %s\n", daemon.LogPath())
		return nil
	}
	fmt.Println(tail)
	return nil
}

// forwardArgs returns the global flags to hand to the background child, as
// `--name=value` pairs so an explicit false survives the hand-off.
// Sub-command arguments are dropped: the child only runs the server.
func forwardArgs(cCtx *cli.Context) []string {
	names := []string{"rtk", "caveman", "ponytail", "auto-update", "no-injection-guard"}
	args := make([]string, 0, len(names))
	for _, name := range names {
		if !cCtx.IsSet(name) {
			continue
		}
		args = append(args, fmt.Sprintf("--%s=%t", name, cCtx.Bool(name)))
	}
	return args
}
