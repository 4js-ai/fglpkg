//go:build !darwin && !linux && !windows

package cli

import "os"

// isTerminal falls back to a character-device check on platforms without a
// dedicated probe. This is the pre-existing behaviour: less precise (it cannot
// tell /dev/null from a real tty) but it keeps the build green everywhere. The
// EOF handling in the init prompts is the safety net against hangs on these
// platforms.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
