package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"9router/proxy/internal/config"
	"9router/proxy/internal/constants"
	"9router/proxy/internal/log"
	"9router/proxy/internal/proc"
)

// StartupTimeout bounds how long Start waits for the daemon to answer /health
// before declaring the spawn a failure.
const StartupTimeout = 20 * time.Second

// stopGraceMS is how long Stop waits for the daemon to exit before killing it.
const stopGraceMS = 5000

// ErrAlreadyRunning is returned when a live daemon already owns the port.
var ErrAlreadyRunning = errors.New("9router-go is already running")
// StopTimeout is how long Restart waits for the old daemon's port to be
// released before giving up.
const StopTimeout = 10 * time.Second

// StartResult reports what a start request did.
type StartResult struct {
	PID      int
	URL      string
	Existing bool
}

// StopResult reports what a stop request did.
type StopResult struct {
	Stopped bool
	PID     int
	Reason  string
}

// Dir returns the directory holding the pid file and the daemon log.
func Dir() string {
	return filepath.Join(config.ResolveDataDir(), "run")
}

// PIDFile returns the path of the pid file for the running daemon.
func PIDFile() string {
	return filepath.Join(Dir(), "gateway.pid")
}

// LogPath returns the path of the background run log.
func LogPath() string {
	return filepath.Join(Dir(), "gateway.log")
}

// RegisterPID records this process as the running daemon, which is what lets
// stop/status/restart find it after the launching terminal is gone. The
// spawning parent writes the same file first, so the daemon overwrites the
// parent's entry with its own rather than waiting for it to go stale.
func RegisterPID() {
	if err := writePID(os.Getpid()); err != nil {
		log.Warn("daemon", "could not record pid", "pid", os.Getpid(), "path", PIDFile(), "error", err)
	}
}

// UnregisterPID removes the pid file, but only when it still names this
// process: the updater's replacement process claims it before the old instance
// exits, and the old one must not delete the new one's claim.
func UnregisterPID() {
	if readPID() == os.Getpid() {
		clearPID()
	}
}

// RunningPID returns the PID of the live daemon, or 0. A pid file left behind
// by a killed process is removed instead of reported.
func RunningPID() int {
	pid := readPID()
	if pid == 0 {
		return 0
	}
	if !proc.Alive(pid) {
		clearPID()
		return 0
	}
	return pid
}

// Start spawns a detached daemon, records its PID, and waits for it to become
// healthy. It returns ErrAlreadyRunning when the port is already served.
func Start(url string, extraArgs []string) (StartResult, error) {
	if busy, who := portBusy(url); busy {
		if pid := RunningPID(); pid > 0 {
			return StartResult{PID: pid, URL: url, Existing: true}, ErrAlreadyRunning
		}
		return StartResult{URL: url, Existing: true}, fmt.Errorf("%w on %s (%s) — stop it first, or start this binary without the daemon", ErrAlreadyRunning, url, who)
	}

	execPath, err := proc.SelfExecutable()
	if err != nil {
		return StartResult{}, fmt.Errorf("daemon.Start: locate binary: %w", err)
	}

	if err := os.MkdirAll(Dir(), constants.FilePermDir); err != nil {
		return StartResult{}, fmt.Errorf("daemon.Start: create run dir: %w", err)
	}

	logFile, err := os.OpenFile(LogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, constants.FilePermFile)
	if err != nil {
		return StartResult{}, fmt.Errorf("daemon.Start: open %s: %w", LogPath(), err)
	}
	defer logFile.Close()

	cmd := exec.Command(execPath, childArgs(extraArgs)...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), EnvBackground+"=1")
	cmd.SysProcAttr = proc.Detached()

	if err := cmd.Start(); err != nil {
		return StartResult{}, fmt.Errorf("daemon.Start: spawn background process: %w", err)
	}
	pid := cmd.Process.Pid

	if err := writePID(pid); err != nil {
		_ = proc.ForceKill(pid)
		return StartResult{PID: pid}, fmt.Errorf("daemon.Start: write pid file: %w", err)
	}

	// The parent must not hold the child: on Windows the process is killed
	// with the parent job, and on POSIX an unreaped child keeps its PID as a
	// zombie. Releasing it lets the daemon outlive this terminal.
	if err := cmd.Process.Release(); err != nil {
		return StartResult{PID: pid}, fmt.Errorf("daemon.Start: release background process: %w", err)
	}

	if err := waitHealthy(url, pid); err != nil {
		clearPID()
		return StartResult{PID: pid}, err
	}

	return StartResult{PID: pid, URL: url}, nil
}

// Stop terminates the daemon, escalating to a kill when it does not exit
// within the graceful window.
func Stop() (StopResult, error) {
	pid := RunningPID()
	if pid == 0 {
		return StopResult{Reason: "not_running"}, nil
	}

	if gone := proc.Terminate(pid, stopGraceMS); !gone {
		clearPID()
		return StopResult{PID: pid}, fmt.Errorf("daemon.Stop: pid %d ignored the stop request and had to be killed", pid)
	}
	clearPID()
	return StopResult{Stopped: true, PID: pid}, nil
}

// Restart replaces the daemon with a fresh one.
//
// The stop happens before the port pre-flight, never after: probing first
// finds the still-listening old daemon and reports "already running", and
// probing again immediately after a kill inherits its socket state.
func Restart(url string, extraArgs []string) (StartResult, error) {
	if pid := RunningPID(); pid > 0 {
		if _, err := Stop(); err != nil {
			return StartResult{PID: pid}, err
		}
		if err := waitPortFree(url); err != nil {
			return StartResult{PID: pid}, err
		}
	}
	return Start(url, extraArgs)
}

// waitPortFree blocks until the listener is gone. TerminateProcess returns as
// soon as the process is told to die; the socket can outlive it for a moment,
// and a start that wins that race loses the port for good.
func waitPortFree(url string) error {
	deadline := time.Now().Add(StopTimeout)
	for {
		busy, _ := portBusy(url)
		if !busy {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon.Restart: %s is still in use after stopping the daemon — is something else listening there?", url)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// LogTail returns the last maxLines lines of the background run log.
func LogTail(maxLines int) string {
	data, err := os.ReadFile(LogPath())
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// childArgs drops the background flag from the forwarded arguments: the child
// runs the server directly and must never try to spawn another daemon.
func childArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == BackgroundFlag || a == BackgroundAlias ||
			strings.HasPrefix(a, BackgroundFlag+"=") ||
			strings.HasPrefix(a, BackgroundAlias+"=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// writePID records the running daemon PID, replacing a stale file.
func writePID(pid int) error {
	if err := os.MkdirAll(Dir(), constants.FilePermDir); err != nil {
		return err
	}
	return os.WriteFile(PIDFile(), []byte(strconv.Itoa(pid)), constants.FilePermFile)
}

// clearPID removes the pid file.
func clearPID() {
	if err := os.Remove(PIDFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("daemon", "remove pid file failed", "path", PIDFile(), "error", err)
	}
}

// readPID parses the pid file, ignoring a missing or malformed one.
func readPID() int {
	data, err := os.ReadFile(PIDFile())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// portBusy reports whether the daemon's port already answers, and what it is.
// The pid file is not a reliable "is it running" oracle: the port outlives the
// daemon that owned it whenever the process is killed rather than stopped, so
// the listening socket is the authority.
func portBusy(url string) (bool, string) {
	host, port, ok := splitHostPort(url)
	if !ok {
		return false, ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return false, ""
	}
	_ = conn.Close()

	if pid := RunningPID(); pid > 0 {
		return true, fmt.Sprintf("pid %d owns it", pid)
	}
	return true, "started outside this binary"
}

// splitHostPort pulls the dialable host and port out of a dashboard URL.
func splitHostPort(url string) (string, string, bool) {
	rest := strings.TrimPrefix(strings.TrimPrefix(url, "http://"), "https://")
	rest = strings.TrimSuffix(strings.SplitN(rest, "/", 2)[0], "/")
	host, port, err := net.SplitHostPort(rest)
	if err != nil {
		return "", "", false
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host, port, true
}
