//go:build !windows

package proc

import (
	"path/filepath"
	"syscall"
	"time"
)

// pidAlive probes a PID with signal 0 (process.kill(pid, 0)).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// requestStop sends SIGTERM, the port's "drain in-flight work and exit" signal.
func requestStop(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}

// forceKill sends SIGKILL.
func forceKill(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}

// resolveLink expands symlinks so a respawn execs the real file.
func resolveLink(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// detached puts the child in its own session so it survives the parent and the
// closing of the terminal that started it.
func detached() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// waitExit polls until the PID is gone or the budget runs out.
func waitExit(pid int, waitMS int) bool {
	deadline := time.Now().Add(time.Duration(waitMS) * time.Millisecond)
	for {
		if !pidAlive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
