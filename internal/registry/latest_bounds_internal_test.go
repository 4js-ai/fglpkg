package registry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestFetchLatestFGLPkgTimesOutOnStalledBody covers the case the transport's
// ResponseHeaderTimeout cannot: a server that sends its headers promptly and then
// never finishes the body. Without an overall deadline this hangs `fglpkg
// self-update` forever (GIS-279 / issue #21 item 3).
func TestFetchLatestFGLPkgTimesOutOnStalledBody(t *testing.T) {
	block := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush() // headers are out; the body never arrives
		<-block
	}))
	defer ts.Close()
	defer close(block)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	old := latestFetchTimeout
	latestFetchTimeout = 200 * time.Millisecond
	defer func() { latestFetchTimeout = old }()

	done := make(chan error, 1)
	go func() { _, err := FetchLatestFGLPkg(); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error from a server that stalls mid-body")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("FetchLatestFGLPkg hung on a stalled response body")
	}
}

// TestFetchLatestFGLPkgRejectsOversizedBody: the endpoint returns a few hundred
// bytes of JSON, so an unbounded body is rejected instead of being read into
// memory.
func TestFetchLatestFGLPkgRejectsOversizedBody(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("A", 4096)))
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_REGISTRY", ts.URL)

	old := maxLatestBytes
	maxLatestBytes = 1024
	defer func() { maxLatestBytes = old }()

	_, err := FetchLatestFGLPkg()
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("want a size-limit error, got %v", err)
	}
}

// TestTransportsKeepHTTP2 guards a subtle regression: setting DialContext on a
// custom Transport turns OFF the automatic HTTP/2 upgrade that
// http.DefaultTransport performs, silently downgrading every registry call to
// HTTP/1.1 unless ForceAttemptHTTP2 is set.
func TestTransportsKeepHTTP2(t *testing.T) {
	tr, ok := httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("registry client transport is %T, want *http.Transport", httpClient.Transport)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 is false: a custom DialContext disables HTTP/2 without it")
	}
}
