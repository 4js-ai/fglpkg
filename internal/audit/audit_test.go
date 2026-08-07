package audit

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// TestAuditEmpty verifies the auditor performs zero HTTP calls when
// there are no JARs to check.
func TestAuditEmpty(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer ts.Close()

	findings, err := Audit(nil, Options{URL: ts.URL})
	if err != nil {
		t.Fatalf("Audit(nil) error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("findings = %d, want 0", len(findings))
	}
	if called {
		t.Error("HTTP endpoint should not be called when no JARs")
	}
}

// TestAuditPerJARQuery feeds the auditor three JARs and asserts the
// stub receives exactly three queries with the expected PURLs.
func TestAuditPerJARQuery(t *testing.T) {
	var (
		mu    sync.Mutex
		purls []string
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Package struct {
				PURL string `json:"purl"`
			} `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		purls = append(purls, req.Package.PURL)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"vulns": []}`)
	}))
	defer ts.Close()

	jars := []lockfile.LockedJAR{
		{GroupID: "g1", ArtifactID: "a1", Version: "1.0.0"},
		{GroupID: "g2", ArtifactID: "a2", Version: "2.0.0"},
		{GroupID: "g3", ArtifactID: "a3", Version: "3.0.0"},
	}
	if _, err := Audit(jars, Options{URL: ts.URL}); err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	if len(purls) != 3 {
		t.Fatalf("queries = %d, want 3; purls %v", len(purls), purls)
	}
	for i, want := range []string{
		"pkg:maven/g1/a1@1.0.0",
		"pkg:maven/g2/a2@2.0.0",
		"pkg:maven/g3/a3@3.0.0",
	} {
		if purls[i] != want {
			t.Errorf("purl[%d] = %q, want %q", i, purls[i], want)
		}
	}
}

// TestAuditSeverityFromGHSA covers the GHSA-severity string mapping
// the OSV.dev response uses.
func TestAuditSeverityFromGHSA(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"CRITICAL", SeverityCritical},
		{"HIGH", SeverityHigh},
		{"MODERATE", SeverityMedium},
		{"MEDIUM", SeverityMedium},
		{"LOW", SeverityLow},
		{"low", SeverityLow}, // case-insensitive
		{"", ""},             // empty input -> empty output (caller picks fallback)
		{"unknown", ""},
	}
	for _, tc := range cases {
		if got := SeverityFromGHSA(tc.in); got != tc.want {
			t.Errorf("SeverityFromGHSA(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSeverityRank(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{SeverityCritical, 4},
		{SeverityHigh, 3},
		{SeverityMedium, 2},
		{SeverityLow, 1},
		{"unknown", 0},
		{"", 0},
	}
	for _, tc := range cases {
		if got := SeverityRank(tc.in); got != tc.want {
			t.Errorf("SeverityRank(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// TestAuditHTTPError verifies a 5xx response surfaces as an error
// rather than being silently treated as a clean tree. MaxAttempts:1
// disables retries so this pins the fail path without backoff delays;
// the retry behavior is covered separately below.
func TestAuditHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := Audit([]lockfile.LockedJAR{
		{GroupID: "g", ArtifactID: "a", Version: "1.0.0"},
	}, Options{URL: ts.URL, MaxAttempts: 1})
	if err == nil {
		t.Fatal("expected error on HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want one mentioning status 500", err)
	}
}

// TestAuditRetriesTransientThenSucceeds: two 503s followed by a clean
// response must resolve to success — a blip no longer aborts the audit.
func TestAuditRetriesTransientThenSucceeds(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n < 3 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, `{"vulns": []}`)
	}))
	defer ts.Close()

	_, err := Audit([]lockfile.LockedJAR{{GroupID: "g", ArtifactID: "a", Version: "1.0.0"}},
		Options{URL: ts.URL, sleep: func(time.Duration) {}})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (two 503s then success)", calls)
	}
}

// TestAuditRetryExhaustedFailsClosed: when every attempt fails the audit
// still errors — a partially-checked tree is never reported as clean.
func TestAuditRetryExhaustedFailsClosed(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	_, err := Audit([]lockfile.LockedJAR{{GroupID: "g", ArtifactID: "a", Version: "1.0.0"}},
		Options{URL: ts.URL, MaxAttempts: 4, sleep: func(time.Duration) {}})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 4 {
		t.Errorf("calls = %d, want 4 (all attempts made)", calls)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("err = %v, want one mentioning 503", err)
	}
}

// TestAuditDoesNotRetryPermanent4xx: a 400 (e.g. malformed PURL) is not
// retryable — retrying cannot help, so it fails on the first attempt.
func TestAuditDoesNotRetryPermanent4xx(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		http.Error(w, "bad purl", http.StatusBadRequest)
	}))
	defer ts.Close()

	_, err := Audit([]lockfile.LockedJAR{{GroupID: "g", ArtifactID: "a", Version: "1.0.0"}},
		Options{URL: ts.URL, sleep: func(time.Duration) {}})
	if err == nil {
		t.Fatal("expected error on 400")
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (400 is permanent, no retry)", calls)
	}
}

// TestAuditHonorsRetryAfter: a 429 carrying Retry-After must delay by
// exactly that interval (overriding exponential backoff) before retrying.
func TestAuditHonorsRetryAfter(t *testing.T) {
	var (
		mu    sync.Mutex
		calls int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 1 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"vulns": []}`)
	}))
	defer ts.Close()

	var slept []time.Duration
	_, err := Audit([]lockfile.LockedJAR{{GroupID: "g", ArtifactID: "a", Version: "1.0.0"}},
		Options{URL: ts.URL, sleep: func(d time.Duration) { slept = append(slept, d) }})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("sleep durations = %v, want [2s] from Retry-After", slept)
	}
}

// TestAuditSeverityFromCVSSWhenGHSAMissing: a vuln with no GHSA label but a
// critical CVSS vector must be classified from the vector (critical), not
// silently defaulted to medium — the core GIS-369 severity-fidelity fix.
func TestAuditSeverityFromCVSSWhenGHSAMissing(t *testing.T) {
	resp := osvResponse{
		Vulns: []osvVulnerability{
			{
				ID:      "OSV-2024-777",
				Summary: "No GHSA label, but a critical CVSS vector",
				Severity: []osvSeverity{
					{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"},
				},
				// DatabaseSpecific.Severity intentionally empty.
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	findings, err := Audit([]lockfile.LockedJAR{{GroupID: "g", ArtifactID: "a", Version: "1.0.0"}},
		Options{URL: ts.URL})
	if err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	if findings[0].Severity != SeverityCritical {
		t.Errorf("Severity = %q, want critical (derived from CVSS 9.8, not the medium default)", findings[0].Severity)
	}
	if findings[0].CVSSScore < 9.0 {
		t.Errorf("CVSSScore = %v, want >= 9.0", findings[0].CVSSScore)
	}
}

// TestAuditMalformedResponse verifies that a non-JSON response is
// surfaced as an error.
func TestAuditMalformedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json")
	}))
	defer ts.Close()

	_, err := Audit([]lockfile.LockedJAR{
		{GroupID: "g", ArtifactID: "a", Version: "1.0.0"},
	}, Options{URL: ts.URL})
	if err == nil {
		t.Fatal("expected error on bad JSON, got nil")
	}
}

// TestAuditFindingsFromOSV runs a full happy-path query that returns
// a vuln with GHSA severity, CVE alias, and an advisory reference.
func TestAuditFindingsFromOSV(t *testing.T) {
	resp := osvResponse{
		Vulns: []osvVulnerability{
			{
				ID:      "GHSA-xxxx-yyyy-zzzz",
				Summary: "Critical XML external entity bug",
				Details: "Long description...",
				Aliases: []string{"CVE-2024-12345", "OSV-2024-1"},
				Severity: []osvSeverity{
					{Type: "CVSS_V3", Score: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"},
				},
				References: []osvRef{
					{Type: "WEB", URL: "https://example.com/blog"},
					{Type: "ADVISORY", URL: "https://github.com/advisories/GHSA-xxxx-yyyy-zzzz"},
				},
				DatabaseSpecific: struct {
					Severity string `json:"severity"`
				}{Severity: "CRITICAL"},
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	findings, err := Audit([]lockfile.LockedJAR{
		{GroupID: "com.example", ArtifactID: "foo", Version: "1.0.0"},
	}, Options{URL: ts.URL})
	if err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want critical", f.Severity)
	}
	if f.CVE != "CVE-2024-12345" {
		t.Errorf("CVE = %q, want CVE-2024-12345 (first CVE alias)", f.CVE)
	}
	if f.ID != "GHSA-xxxx-yyyy-zzzz" {
		t.Errorf("ID = %q, want GHSA-xxxx-yyyy-zzzz", f.ID)
	}
	if !strings.Contains(f.Reference, "github.com/advisories") {
		t.Errorf("Reference = %q, want the ADVISORY URL (not the WEB one)", f.Reference)
	}
	if f.CVSSVector == "" {
		t.Error("CVSSVector should be populated")
	}
	if f.Coordinate != "pkg:maven/com.example/foo@1.0.0" {
		t.Errorf("Coordinate = %q", f.Coordinate)
	}
}

// TestAuditUnknownSeverityDefaultsToMedium ensures that a vuln with
// no GHSA severity label is still surfaced at the default --severity
// floor (medium) rather than silently treated as "low" and skipped.
func TestAuditUnknownSeverityDefaultsToMedium(t *testing.T) {
	resp := osvResponse{
		Vulns: []osvVulnerability{
			{
				ID:      "OSV-2024-999",
				Summary: "Unlabeled",
				// DatabaseSpecific.Severity intentionally empty.
			},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	findings, err := Audit([]lockfile.LockedJAR{
		{GroupID: "g", ArtifactID: "a", Version: "1.0.0"},
	}, Options{URL: ts.URL})
	if err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	if len(findings) != 1 || findings[0].Severity != SeverityMedium {
		t.Errorf("findings = %+v, want a single medium-severity entry", findings)
	}
}

// TestAuditDedupsCoordinates verifies that the same coordinate
// appearing in the input twice is only queried once.
func TestAuditDedupsCoordinates(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `{"vulns": []}`)
	}))
	defer ts.Close()

	jars := []lockfile.LockedJAR{
		{GroupID: "g", ArtifactID: "a", Version: "1.0.0"},
		{GroupID: "g", ArtifactID: "a", Version: "1.0.0"},
	}
	if _, err := Audit(jars, Options{URL: ts.URL}); err != nil {
		t.Fatalf("Audit error: %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (duplicate should be filtered)", calls)
	}
}

// Sanity: confirm SourceLabel is the value the CLI expects to emit.
func TestSourceLabel(t *testing.T) {
	if SourceLabel != "osv.dev" {
		t.Errorf("SourceLabel = %q, want osv.dev", SourceLabel)
	}
}
