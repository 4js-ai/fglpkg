//go:build linux

package cli

import "syscall"

// ioctlReadTermios is the request code that reads terminal attributes on
// Linux (TCGETS). See isatty_unix.go.
const ioctlReadTermios uintptr = syscall.TCGETS
