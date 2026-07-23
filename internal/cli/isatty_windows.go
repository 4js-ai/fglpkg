//go:build windows

package cli

import (
	"os"
	"syscall"
	"unsafe"
)

// GetConsoleMode succeeds only for a handle attached to a real console, which is
// the Windows equivalent of the isatty probe. Declared directly against
// kernel32 to keep the module dependency-free.
var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
)

// isTerminal reports whether f is attached to a Windows console (not a pipe,
// a redirected file, or NUL).
func isTerminal(f *os.File) bool {
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(f.Fd(), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
