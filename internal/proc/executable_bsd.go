//go:build freebsd || netbsd || openbsd || dragonfly

package proc

import (
	"fmt"
	"strconv"
	"syscall"
)

// The BSDs expose the image path as a per-pid sysctl. There is no
// /proc/<pid>/exe here, and QueryFullProcessImageName is Windows-only, so this
// is the platform's own answer to the same question. macOS is deliberately not
// in this build tag: it has no kern.proc.pathname OID at all, and uses
// proc_pidpath instead — see executable_darwin.go.
func executablePath(pid int) (string, error) {
	// The value is a NUL-terminated char array, so a raw string can carry a
	// trailing NUL that never compares equal to SelfExecutable's.
	buf, err := syscall.Sysctl("kern.proc.pathname." + strconv.Itoa(pid))
	if err != nil {
		return "", fmt.Errorf("sysctl kern.proc.pathname.%d: %w", pid, err)
	}
	return trimNUL(buf), nil
}

