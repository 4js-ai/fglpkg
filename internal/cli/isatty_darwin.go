//go:build darwin

package cli

import "syscall"

// ioctlReadTermios is the request code that reads terminal attributes on
// Darwin/BSD (TIOCGETA). See isatty_unix.go.
const ioctlReadTermios uintptr = syscall.TIOCGETA
