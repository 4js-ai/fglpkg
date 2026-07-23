//go:build darwin || linux

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is attached to an interactive terminal.
//
// It issues the terminal-attributes ioctl — the classic isatty(3) probe — which
// succeeds only for real ttys. This is deliberately stricter than checking
// os.ModeCharDevice: non-tty character devices such as /dev/null also carry the
// char-device bit, so a mode-only check would misreport `fglpkg init < /dev/null`
// (and CI stdin) as interactive and prompt into a void.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		ioctlReadTermios,
		uintptr(unsafe.Pointer(&t)),
	)
	return errno == 0
}
