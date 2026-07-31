package updatecheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func baseEnv() Env {
	return Env{
		Version:     "3.3.0",
		Command:     "install",
		StdoutIsTTY: true,
		StderrIsTTY: true,
		Enabled:     true,
		Interval:    24 * time.Hour,
		Now:         time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC),
	}
}

func TestShouldCheck(t *testing.T) {
	if !ShouldCheck(baseEnv()) {
		t.Fatal("base env should check")
	}
	cases := map[string]func(*Env){
		"dev version":     func(e *Env) { e.Version = "dev" },
		"empty version":   func(e *Env) { e.Version = "" },
		"CI":              func(e *Env) { e.CI = true },
		"env opt-out":     func(e *Env) { e.NoCheckEnv = true },
		"config disabled": func(e *Env) { e.Enabled = false },
		"not a TTY":       func(e *Env) { e.StdoutIsTTY = false },
		"self-update cmd": func(e *Env) { e.Command = "self-update" },
		"upgrade cmd":     func(e *Env) { e.Command = "upgrade" },
		"version cmd":     func(e *Env) { e.Command = "version" },
		"within interval": func(e *Env) { e.LastCheck = e.Now.Add(-time.Hour) },
	}
	for name, mut := range cases {
		e := baseEnv()
		mut(&e)
		if ShouldCheck(e) {
			t.Errorf("%s: should NOT check", name)
		}
	}
	// Past the interval, it checks again.
	e := baseEnv()
	e.LastCheck = e.Now.Add(-48 * time.Hour)
	if !ShouldCheck(e) {
		t.Error("past interval: should check")
	}
}

// TestAllowedIgnoresInterval pins the split: the interval throttles the network
// refresh only, so a run within the interval is still Allowed and can therefore
// print a notice from cache. A redirected stderr does not disable the feature —
// that is decided at print time, not here.
func TestAllowedIgnoresInterval(t *testing.T) {
	e := baseEnv()
	e.LastCheck = e.Now.Add(-time.Hour)
	if !Allowed(e) {
		t.Error("within the interval: still Allowed (notice-from-cache)")
	}
	if ShouldCheck(e) {
		t.Error("within the interval: should NOT refresh")
	}
	e.StderrIsTTY = false
	if !Allowed(e) {
		t.Error("stderr not a TTY: Allowed is about stdout, not stderr")
	}
	e.StdoutIsTTY = false
	if Allowed(e) {
		t.Error("stdout not a TTY: not Allowed")
	}
}

func TestStateRoundTrip(t *testing.T) {
	home := t.TempDir()
	if got := LoadState(home); !got.LastCheck.IsZero() || got.LatestKnown != "" {
		t.Errorf("missing file should be zero State, got %+v", got)
	}
	want := State{LastCheck: time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC), LatestKnown: "3.4.0"}
	if err := SaveState(home, want); err != nil {
		t.Fatal(err)
	}
	got := LoadState(home)
	if !got.LastCheck.Equal(want.LastCheck) || got.LatestKnown != want.LatestKnown {
		t.Errorf("round-trip: got %+v, want %+v", got, want)
	}
}

func newerByString(cur, lat string) bool { return cur != lat }

func TestFinishPrintsWhenReady(t *testing.T) {
	p := &Pending{current: "3.3.0", stderrTTY: true, ch: make(chan string, 1)}
	p.ch <- "3.4.0"
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if !strings.Contains(buf.String(), "3.3.0 → 3.4.0") {
		t.Errorf("expected upgrade notice, got %q", buf.String())
	}
}

// TestFinishFallsBackToCache is the core of the fix: a refresh that has not come
// back (or was throttled away entirely) must not silence the notice — the cached
// version is announced instead, with no network involved.
func TestFinishFallsBackToCache(t *testing.T) {
	// In-flight refresh, no result yet.
	p := &Pending{current: "3.3.0", cached: "3.4.0", stderrTTY: true, ch: make(chan string, 1)}
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if !strings.Contains(buf.String(), "3.3.0 → 3.4.0") {
		t.Errorf("in-flight refresh: want cached notice, got %q", buf.String())
	}
	// Throttled run: no channel at all.
	p = &Pending{current: "3.3.0", cached: "3.4.0", stderrTTY: true}
	buf.Reset()
	p.Finish(&buf, newerByString)
	if !strings.Contains(buf.String(), "3.3.0 → 3.4.0") {
		t.Errorf("throttled run: want cached notice, got %q", buf.String())
	}
}

// TestFinishFreshResultWins checks that exactly one notice is printed and that it
// carries the fresher of the two known versions.
func TestFinishFreshResultWins(t *testing.T) {
	p := &Pending{current: "3.3.0", cached: "3.4.0", stderrTTY: true, ch: make(chan string, 1)}
	p.ch <- "3.5.0"
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	out := buf.String()
	if !strings.Contains(out, "3.3.0 → 3.5.0") {
		t.Errorf("want the fresh version in the notice, got %q", out)
	}
	if strings.Count(out, "A new fglpkg is available") != 1 {
		t.Errorf("want exactly one notice, got %q", out)
	}
}

func TestFinishSkipsWhenNothingKnown(t *testing.T) {
	p := &Pending{current: "3.3.0", stderrTTY: true, ch: make(chan string, 1)} // empty channel, empty cache
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if buf.Len() != 0 {
		t.Errorf("expected no output with no known version, got %q", buf.String())
	}
}

// TestFinishSilentWhenStderrRedirected: the notice goes to stderr, so a
// redirected stderr means nobody would see it — don't write into the redirect.
func TestFinishSilentWhenStderrRedirected(t *testing.T) {
	p := &Pending{current: "3.3.0", cached: "3.4.0", stderrTTY: false}
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if buf.Len() != 0 {
		t.Errorf("expected no output when stderr is not a TTY, got %q", buf.String())
	}
}

func TestFinishNilNoop(t *testing.T) {
	var p *Pending
	var buf bytes.Buffer
	p.Finish(&buf, newerByString) // must not panic
	if buf.Len() != 0 {
		t.Errorf("nil Finish should print nothing, got %q", buf.String())
	}
}

func TestBeginRunsAndCaches(t *testing.T) {
	home := t.TempDir()
	p := Begin(home, baseEnv(), State{}, func() (string, error) { return "3.4.0", nil })
	if p == nil {
		t.Fatal("Begin returned nil for a should-check env")
	}
	// Reading the channel waits for the goroutine, which writes the cache before
	// sending — so this is deterministic, not a sleep.
	if got := <-p.ch; got != "3.4.0" {
		t.Errorf("channel got %q, want 3.4.0", got)
	}
	if st := LoadState(home); st.LatestKnown != "3.4.0" || st.LastCheck.IsZero() {
		t.Errorf("cache not updated: %+v", st)
	}
}

func TestBeginSkipReturnsNil(t *testing.T) {
	e := baseEnv()
	e.Enabled = false
	if p := Begin(t.TempDir(), e, State{}, func() (string, error) { return "3.4.0", nil }); p != nil {
		t.Error("Begin should return nil when the feature is disabled")
	}
}

func TestBeginPreservesPrevOnFetchError(t *testing.T) {
	home := t.TempDir()
	p := Begin(home, baseEnv(), State{LatestKnown: "3.2.0"}, func() (string, error) { return "", errors.New("boom") })
	if p == nil {
		t.Fatal("Begin returned nil")
	}
	if got := <-p.ch; got != "" {
		t.Errorf("channel got %q, want empty on fetch error", got)
	}
	st := LoadState(home)
	if st.LatestKnown != "3.2.0" {
		t.Errorf("prev version not preserved on error: %+v", st)
	}
	if st.LastCheck.IsZero() {
		t.Error("attempt time should be recorded even on error (backoff)")
	}
}

// TestBeginPersistsAttemptBeforeFetch is the throttle fix (issue #21 item 1): a
// fast command exits before the background fetch returns, so the attempt time
// must already be on disk when Begin returns. Otherwise every `fglpkg list` looks
// stale and fires a fresh registry request.
func TestBeginPersistsAttemptBeforeFetch(t *testing.T) {
	home := t.TempDir()
	blocked := make(chan struct{})
	defer close(blocked) // let the goroutine exit at test end
	e := baseEnv()
	p := Begin(home, e, State{LatestKnown: "3.2.0"}, func() (string, error) {
		<-blocked // never returns while the test runs
		return "", nil
	})
	if p == nil {
		t.Fatal("Begin returned nil")
	}
	st := LoadState(home)
	if !st.LastCheck.Equal(e.Now) {
		t.Errorf("lastUpdateCheck = %v, want %v written synchronously by Begin", st.LastCheck, e.Now)
	}
	if st.LatestKnown != "3.2.0" {
		t.Errorf("LatestKnown = %q, want the previous value preserved", st.LatestKnown)
	}
	// And the throttle now holds for the next invocation.
	next := baseEnv()
	next.LastCheck = st.LastCheck
	next.Now = st.LastCheck.Add(time.Minute)
	if ShouldCheck(next) {
		t.Error("next invocation should be throttled by the persisted attempt time")
	}
}

// TestBeginPersistsOnFreshInstall: on a brand-new install nothing has created
// ~/.fglpkg yet. The state write must create it, otherwise the attempt is never
// recorded and a new user's every command re-checks — item 1's symptom, just with
// a different cause.
func TestBeginPersistsOnFreshInstall(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".fglpkg") // deliberately absent
	e := baseEnv()
	p := Begin(home, e, State{}, func() (string, error) { return "3.4.0", nil })
	if p == nil {
		t.Fatal("Begin returned nil")
	}
	if st := LoadState(home); !st.LastCheck.Equal(e.Now) {
		t.Errorf("attempt not persisted into a missing home: %+v", st)
	}
	if got := <-p.ch; got != "3.4.0" {
		t.Fatalf("channel got %q", got)
	}
	if st := LoadState(home); st.LatestKnown != "3.4.0" {
		t.Errorf("result not persisted into a missing home: %+v", st)
	}
}

// TestBeginThrottledNotifiesFromCache: within the interval there is no fetch at
// all, yet a known-newer cached version is still announced.
func TestBeginThrottledNotifiesFromCache(t *testing.T) {
	home := t.TempDir()
	e := baseEnv()
	e.LastCheck = e.Now.Add(-time.Hour)
	p := Begin(home, e, State{LastCheck: e.LastCheck, LatestKnown: "3.4.0"}, func() (string, error) {
		t.Error("fetch must not run within the interval")
		return "9.9.9", nil
	})
	if p == nil {
		t.Fatal("Begin returned nil for a throttled-but-allowed env")
	}
	if p.ch != nil {
		t.Error("throttled run should not start a refresh")
	}
	var buf bytes.Buffer
	p.Finish(&buf, newerByString)
	if !strings.Contains(buf.String(), "3.3.0 → 3.4.0") {
		t.Errorf("want a cached notice on a throttled run, got %q", buf.String())
	}
	// The throttled run must not touch the cache.
	if st := LoadState(home); !st.LastCheck.IsZero() {
		t.Errorf("throttled run rewrote the cache: %+v", st)
	}
}

// TestSaveStateConcurrent guards the write-isolation property (issue #21 item 5):
// two invocations writing the cache at once each go through their OWN temp file,
// so the result is always one writer's complete state — never a mix of both — and
// no temp files are left behind.
//
// A concurrent write is allowed to FAIL (on Windows, two MoveFileEx replaces
// racing for the same destination can return "Access is denied"); every SaveState
// caller ignores the error because the cache is best-effort, and a lost update
// only means the next run re-checks. What must never happen is a corrupt file.
func TestSaveStateConcurrent(t *testing.T) {
	home := t.TempDir()
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	var wg sync.WaitGroup
	for i, v := range []string{"3.4.0", "3.5.0"} {
		wg.Add(1)
		go func(i int, v string) {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				_ = SaveState(home, State{LastCheck: base.Add(time.Duration(i) * time.Hour), LatestKnown: v})
			}
		}(i, v)
	}
	wg.Wait()
	// LoadState swallows a parse error into a zero State, so read the raw bytes:
	// a torn write must be detectable, not silently normalised away.
	data, err := os.ReadFile(filepath.Join(home, StateFilename))
	if err != nil {
		t.Fatalf("cache missing after concurrent writes: %v", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("cache is not valid JSON after concurrent writes (%v): %s", err, data)
	}
	switch {
	case st.LatestKnown == "3.4.0" && st.LastCheck.Equal(base):
	case st.LatestKnown == "3.5.0" && st.LastCheck.Equal(base.Add(time.Hour)):
	default:
		t.Errorf("cache is not either writer's complete state: %+v", st)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != StateFilename {
			t.Errorf("leftover temp file in home: %s", e.Name())
		}
	}
}
