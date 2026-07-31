// Package updatecheck implements fglpkg's passive "a new version is available"
// notice (GIS-255). It piggybacks on ordinary command runs — no daemon — and is
// designed to never block the command, never change its exit code, and never
// surface an error to the user (network failures are swallowed).
//
// Notifying and refreshing are separate concerns: every allowed run announces a
// known-newer version straight from the cache (instant, works offline), while the
// network refresh happens at most once per interval and only ever feeds the cache
// for later runs.
//
// User settings (opt-out, interval) live in config.json and are read-only. The
// mutable cache — last check time and last seen version — lives here, in a
// tool-managed ~/.fglpkg/update-check.json (mode 0600, atomic writes), so the
// feature never rewrites the user's hand-edited registry config.
package updatecheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/atomicfile"
)

// StateFilename is the tool-managed cache file under the fglpkg home.
const StateFilename = "update-check.json"

// State is the cache persisted in update-check.json.
type State struct {
	LastCheck   time.Time `json:"lastUpdateCheck"`
	LatestKnown string    `json:"latestKnownVersion"`
}

func statePath(home string) string { return filepath.Join(home, StateFilename) }

// LoadState reads update-check.json. A missing, blank, or corrupt file yields a
// zero State — the check is best-effort, so it never hard-fails.
func LoadState(home string) State {
	data, err := os.ReadFile(statePath(home))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return State{}
	}
	var s State
	if json.Unmarshal(data, &s) != nil {
		return State{}
	}
	return s
}

// SaveState atomically writes update-check.json (mode 0600). The write goes to a
// sibling temp file and is renamed into place, so a process killed mid-write
// (e.g. a fast command exiting before the background fetch returns) leaves the
// existing cache intact rather than a truncated file.
//
// The home directory is created if missing: atomicfile requires an existing
// parent, and on a fresh install nothing else has created ~/.fglpkg yet. Without
// this the throttle could never engage for a brand-new user — every run would
// fail to record its attempt and re-check.
func SaveState(home string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(statePath(home), data, 0o600)
}

// Env captures every input to the gating decisions, so Allowed and ShouldCheck
// are pure functions — testable without a real environment, TTY, or clock.
type Env struct {
	Version     string        // cli.Version; "" or "dev" disables (source build)
	Command     string        // the invoked subcommand
	CI          bool          // $CI is set
	NoCheckEnv  bool          // $FGLPKG_NO_UPDATE_CHECK is set
	StdoutIsTTY bool          // stdout is the data stream: not a TTY => scripted use, stay out of it
	StderrIsTTY bool          // the notice goes to stderr; don't write it into a redirected stream
	Enabled     bool          // config.json updateCheck
	Interval    time.Duration // config.json updateCheckInterval
	Now         time.Time
	LastCheck   time.Time // from the cached State
}

// Allowed reports whether the passive feature may act at all this invocation,
// ignoring the interval: a released build, no opt-out, and interactive use.
// It gates both halves — the cached notice and the background refresh.
//
// The TTY condition is deliberately about STDOUT even though the notice is
// written to stderr: stdout is the command's data stream, so a redirected stdout
// is the signal that this run is scripted, and a scripted run should neither
// touch the network nor emit advisory chatter. Whether the notice can actually
// be seen is a separate question, decided by StderrIsTTY at print time.
func Allowed(e Env) bool {
	if e.Version == "" || e.Version == "dev" {
		return false
	}
	if e.CI || e.NoCheckEnv || !e.Enabled {
		return false
	}
	if !e.StdoutIsTTY {
		return false
	}
	switch e.Command {
	case "self-update", "upgrade", "version", "--version", "-v", "-V":
		return false
	}
	return true
}

// ShouldCheck reports whether the network refresh should run this invocation —
// Allowed plus a stale cache. A notice can still be printed from cache when this
// is false; that is the point of the split.
func ShouldCheck(e Env) bool {
	if !Allowed(e) {
		return false
	}
	if !e.LastCheck.IsZero() && e.Now.Sub(e.LastCheck) < e.Interval {
		return false
	}
	return true
}

// Pending is this invocation's passive-check handle: the version already known
// from cache, plus (when the interval elapsed) an in-flight background refresh.
type Pending struct {
	current   string
	cached    string      // LatestKnown from the cache; printed if no fresh result arrives
	stderrTTY bool        // print only into a real terminal
	ch        chan string // nil when the refresh was throttled
}

// Begin sets up the passive update check for this invocation. It never blocks
// and returns nil when the feature is not allowed at all (a nil *Pending is safe
// to Finish).
//
// Two independent concerns, per specs/self-update.md:
//
//   - Notify: the cached LatestKnown is carried into the returned handle, so a
//     known-newer version is announced by Finish immediately — no network, works
//     offline, and works on throttled runs.
//   - Refresh: when the interval has elapsed, the attempt time is persisted
//     SYNCHRONOUSLY here, before the fetch starts, and only then is the fetch
//     backgrounded. Recording the attempt up front is what makes the throttle
//     hold for fast commands: `fglpkg list` can exit before the goroutine gets a
//     chance to write, and a LastCheck written only by the goroutine would leave
//     every fast invocation looking stale and firing a fresh request.
//
// fetch returns the latest version string (registry.FetchLatestFGLPkg().Version
// in production). On failure the previously cached version is kept, so a flaky
// endpoint neither clears the cache nor gets hammered.
func Begin(home string, e Env, state State, fetch func() (string, error)) *Pending {
	if !Allowed(e) {
		return nil
	}
	p := &Pending{current: e.Version, cached: state.LatestKnown, stderrTTY: e.StderrIsTTY}
	if !ShouldCheck(e) {
		return p // throttled: notice-from-cache only
	}
	now := e.Now
	_ = SaveState(home, State{LastCheck: now, LatestKnown: state.LatestKnown})
	p.ch = make(chan string, 1)
	go func() {
		latest, err := fetch()
		if err != nil || latest == "" {
			p.ch <- "" // attempt time is already on disk
			return
		}
		_ = SaveState(home, State{LastCheck: now, LatestKnown: latest})
		p.ch <- latest
	}()
	return p
}

// Finish prints the one-line notice to w if a newer version is known —
// newer(current, latest) decides. It never blocks: a background refresh that has
// ALREADY returned wins (it is the freshest answer), otherwise the cached
// version is used, so the notice is instant and offline-capable. Exactly one
// notice is printed at most. Safe to call on a nil *Pending.
func (p *Pending) Finish(w io.Writer, newer func(current, latest string) bool) {
	if p == nil || !p.stderrTTY {
		return
	}
	latest := p.cached
	if p.ch != nil {
		select {
		case fresh := <-p.ch:
			if fresh != "" {
				latest = fresh
			}
		default:
			// Refresh not back yet — fall back to the cache, per the
			// non-blocking contract. The goroutine still updates the cache.
		}
	}
	if latest != "" && newer(p.current, latest) {
		printNotice(w, p.current, latest)
	}
}

func printNotice(w io.Writer, current, latest string) {
	bar := strings.Repeat("─", 45)
	fmt.Fprintf(w, "\n%s\n A new fglpkg is available: %s → %s\n Run 'fglpkg self-update' to upgrade.\n%s\n",
		bar, current, latest, bar)
}
