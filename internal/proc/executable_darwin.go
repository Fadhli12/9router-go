//go:build darwin

package proc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// macOS does not have the kern.proc.pathname sysctl the BSDs answer this
// question with — that OID simply does not exist here, so the shared BSD path
// failed for every pid and the daemon could never recognise its own process.
// The platform's own answer is libproc's proc_pidpath(), which is a thin
// wrapper over the proc_info syscall:
//
//	__proc_info(PROC_INFO_CALL_PIDINFO, pid, PROC_PIDPATHINFO, 0, buf, bufsize)
//
// It is called directly rather than through cgo because the build must stay
// CGO_ENABLED=0 for every release target.
const (
	// procInfoCallPIDInfo is PROC_INFO_CALL_PIDINFO from sys/proc_info.h.
	procInfoCallPIDInfo = 2
	// procPIDPathInfo is PROC_PIDPATHINFO, the flavor returning the image path.
	procPIDPathInfo = 11
	// procPIDPathInfoMaxSize is PROC_PIDPATHINFO_MAXSIZE (4 * MAXPATHLEN). The
	// syscall rejects a smaller buffer than PROC_PIDPATHINFO_SIZE, so this is
	// both the ceiling and a safe size to always pass.
	procPIDPathInfoMaxSize = 4096
)

func executablePath(pid int) (string, error) {
	buf := make([]byte, procPIDPathInfoMaxSize)
	// The return value is not a length: on success proc_info leaves a
	// NUL-terminated path in the buffer and returns 0 for this flavor, so
	// treating the result as a byte count reads every success as a failure.
	//
	// SYS_PROC_INFO is marked deprecated in x/sys in favour of the libSystem
	// wrapper, but reaching that wrapper needs cgo, and the build is
	// CGO_ENABLED=0 for every release target (AGENTS.md §4.F). The syscall is
	// the only pure-Go route to this path, so the finding is a deliberate
	// choice rather than an omission.
	_, _, errno := unix.Syscall6(
		unix.SYS_PROC_INFO, //nolint:staticcheck // SA1019: the libSystem wrapper needs cgo; §4.F forbids it
		procInfoCallPIDInfo,
		uintptr(pid),
		procPIDPathInfo,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if errno != 0 {
		return "", fmt.Errorf("proc_pidpath pid %d: %w", pid, errno)
	}
	path := trimNUL(string(buf))
	if path == "" {
		return "", fmt.Errorf("proc_pidpath pid %d: empty image path", pid)
	}
	return path, nil
}
