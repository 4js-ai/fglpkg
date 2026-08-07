// Package audit cross-checks installed Java JAR dependencies against a
// public vulnerability database (OSV.dev) and returns findings.
// v1 is report-only and queries OSV.dev only; BDL packages are not
// covered yet because no public CVE feed indexes them.
package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

const (
	// defaultURL is the OSV.dev single-package query endpoint. It returns
	// full vulnerability details (id, summary, severity, references) so
	// no follow-up requests are needed per finding.
	defaultURL = "https://api.osv.dev/v1/query"

	defaultTimeout = 30 * time.Second

	// defaultMaxAttempts is how many times each coordinate is queried before
	// giving up (1 initial try + retries). Retries apply only to transient
	// failures (429/5xx/transport); a persistent failure still fails closed.
	defaultMaxAttempts = 3

	// defaultBackoff is the base delay between retries. The nth retry waits
	// defaultBackoff * 2^(n-1), unless the server sent a Retry-After.
	defaultBackoff = 250 * time.Millisecond

	// maxRetryAfter caps how long a server-supplied Retry-After can stall a
	// single retry, so a hostile or misconfigured header cannot hang the CLI.
	maxRetryAfter = 30 * time.Second

	// SourceLabel is the value emitted as `source` in the JSON report.
	SourceLabel = "osv.dev"
)

// Severity buckets, ordered from least to most severe. SeverityRank
// returns the ordinal; callers compare against a threshold to decide
// whether a finding fails the build.
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"
)

// Finding is one vulnerability against one JAR coordinate.
type Finding struct {
	Coordinate  string  `json:"coordinate"` // pkg:maven/<groupId>/<artifactId>@<version>
	GroupID     string  `json:"groupId"`
	ArtifactID  string  `json:"artifactId"`
	Version     string  `json:"version"`
	ID          string  `json:"id"` // OSV/GHSA advisory id
	CVE         string  `json:"cve,omitempty"`
	Title       string  `json:"title"`
	Description string  `json:"description,omitempty"`
	CVSSScore   float64 `json:"cvssScore,omitempty"`
	CVSSVector  string  `json:"cvssVector,omitempty"`
	Severity    string  `json:"severity"` // critical|high|medium|low
	Reference   string  `json:"reference,omitempty"`
}

// Options configure an Audit call. Zero values pick sensible defaults
// suitable for production use; tests inject URL and HTTPClient.
type Options struct {
	URL        string
	HTTPClient *http.Client

	// MaxAttempts caps how many times each coordinate is queried before
	// giving up (1 initial try + retries). 0 → defaultMaxAttempts. Set to 1
	// to disable retries.
	MaxAttempts int

	// backoff is the base retry delay; the nth retry waits backoff*2^(n-1).
	// 0 → defaultBackoff. Unexported so external callers get the production
	// cadence; in-package tests set it (with sleep) to run instantly.
	backoff time.Duration

	// sleep performs the inter-retry delay. nil → time.Sleep. Tests inject a
	// no-op so retry paths run without real waits.
	sleep func(time.Duration)
}

// Audit queries the advisory service for every JAR in jars and returns
// the resulting findings. Returns a non-nil error if the service is
// unreachable, returns a non-2xx status, or yields malformed JSON;
// callers must treat any error as "audit failed" rather than "no
// findings", since a partial-failure report is worse than no report.
//
// OSV.dev's /v1/query endpoint takes one package per request, so this
// makes one HTTP call per deduplicated JAR coordinate. For typical
// projects (≤ ~30 JARs) this completes well under the configured
// timeout. A future revision may use /v1/querybatch + parallel detail
// fetches for larger trees.
//
// Transient failures (HTTP 429, 5xx, transport errors) are retried with
// exponential backoff (honoring Retry-After) before the coordinate is
// declared failed — a single blip no longer aborts the whole audit.
// Persistent failures still fail closed, per the contract above.
func Audit(jars []lockfile.LockedJAR, opts Options) ([]Finding, error) {
	if len(jars) == 0 {
		return nil, nil
	}

	url := opts.URL
	if url == "" {
		url = defaultURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	backoff := opts.backoff
	if backoff <= 0 {
		backoff = defaultBackoff
	}
	sleep := opts.sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	// Dedup coordinates so the same JAR is not queried twice.
	type query struct {
		purl string
		jar  lockfile.LockedJAR
	}
	seen := make(map[string]bool, len(jars))
	queries := make([]query, 0, len(jars))
	for _, j := range jars {
		purl := mavenPURL(j.GroupID, j.ArtifactID, j.Version)
		if seen[purl] {
			continue
		}
		seen[purl] = true
		queries = append(queries, query{purl: purl, jar: j})
	}

	var findings []Finding
	for _, q := range queries {
		resp, err := queryOSVWithRetry(client, url, q.purl, maxAttempts, backoff, sleep)
		if err != nil {
			return nil, err
		}
		for _, v := range resp.Vulns {
			findings = append(findings, vulnToFinding(q, v))
		}
	}
	return findings, nil
}

// queryOSVWithRetry calls queryOSV, retrying transient failures (429, 5xx,
// transport errors) up to maxAttempts with exponential backoff, honoring a
// server-supplied Retry-After when present. Permanent failures (4xx other than
// 429, malformed JSON) return immediately — retrying them cannot help. When
// every attempt fails the last error is returned so the caller still fails
// closed: a partially-checked tree is never reported as clean.
func queryOSVWithRetry(client *http.Client, url, purl string, maxAttempts int, backoff time.Duration, sleep func(time.Duration)) (*osvResponse, error) {
	var lastErr error
	for attempt := 1; ; attempt++ {
		resp, err := queryOSV(client, url, purl)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		var tr *transientError
		if !errors.As(err, &tr) || attempt >= maxAttempts {
			return nil, lastErr
		}
		delay := backoff << (attempt - 1)
		if tr.retryAfter > 0 {
			delay = tr.retryAfter
		}
		sleep(delay)
	}
}

// transientError marks a download failure worth retrying. retryAfter, when
// > 0, is a server-requested delay (from the Retry-After header) that overrides
// the client's exponential backoff.
type transientError struct {
	err        error
	retryAfter time.Duration
}

func (e *transientError) Error() string { return e.err.Error() }
func (e *transientError) Unwrap() error { return e.err }

// parseRetryAfter reads a Retry-After header in delta-seconds form and returns
// it capped at maxRetryAfter. The HTTP-date form and unparseable values yield
// 0, letting the caller fall back to exponential backoff.
func parseRetryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	secs, err := strconv.Atoi(h)
	if err != nil || secs < 0 {
		return 0
	}
	d := time.Duration(secs) * time.Second
	if d > maxRetryAfter {
		return maxRetryAfter
	}
	return d
}

// SeverityFromGHSA maps the OSV `database_specific.severity` string
// (used by GHSA-sourced entries) to one of the four canonical buckets.
// Returns the empty string when the input is unrecognized, so callers
// can fall back to other signals.
func SeverityFromGHSA(s string) string {
	switch strings.ToUpper(s) {
	case "CRITICAL":
		return SeverityCritical
	case "HIGH":
		return SeverityHigh
	case "MODERATE", "MEDIUM":
		return SeverityMedium
	case "LOW":
		return SeverityLow
	}
	return ""
}

// SeverityRank returns an ordinal for severity strings so callers can
// compare against a threshold: low=1, medium=2, high=3, critical=4.
// An unrecognized severity yields 0, which is below every valid threshold.
func SeverityRank(sev string) int {
	switch sev {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	}
	return 0
}

// ValidSeverity reports whether sev is one of the four recognized
// severity strings.
func ValidSeverity(sev string) bool {
	return SeverityRank(sev) > 0
}

func mavenPURL(group, artifact, version string) string {
	return "pkg:maven/" + group + "/" + artifact + "@" + version
}

// vulnToFinding converts an OSV vulnerability record into our Finding
// shape. Severity comes from `database_specific.severity` when present
// (GHSA always sets it); otherwise the finding is reported with an
// empty severity bucket, which SeverityRank treats as below every
// threshold — better to surface the finding than to silently demote it
// below the floor and fail closed.
func vulnToFinding(q struct {
	purl string
	jar  lockfile.LockedJAR
}, v osvVulnerability) Finding {
	f := Finding{
		Coordinate:  q.purl,
		GroupID:     q.jar.GroupID,
		ArtifactID:  q.jar.ArtifactID,
		Version:     q.jar.Version,
		ID:          v.ID,
		Title:       v.Summary,
		Description: v.Details,
		Severity:    SeverityFromGHSA(v.DatabaseSpecific.Severity),
	}
	// Prefer the first CVE alias as a human-friendly identifier.
	for _, a := range v.Aliases {
		if strings.HasPrefix(a, "CVE-") {
			f.CVE = a
			break
		}
	}
	// Prefer ADVISORY references; fall back to the first reference.
	for _, r := range v.References {
		if strings.EqualFold(r.Type, "ADVISORY") && r.URL != "" {
			f.Reference = r.URL
			break
		}
	}
	if f.Reference == "" {
		for _, r := range v.References {
			if r.URL != "" {
				f.Reference = r.URL
				break
			}
		}
	}
	// Parse the CVSS vector(s). OSV may list several (v2, v3, v4); keep the
	// highest base score we can compute and its vector string. An unscorable
	// vector (v2/v4, or malformed) still populates CVSSVector for display but
	// leaves CVSSScore 0 and does not drive severity.
	var scored bool
	for _, s := range v.Severity {
		if s.Score == "" {
			continue
		}
		if f.CVSSVector == "" {
			f.CVSSVector = s.Score
		}
		if score, ok := cvssBaseScore(s.Score); ok && (!scored || score > f.CVSSScore) {
			f.CVSSScore, f.CVSSVector, scored = score, s.Score, true
		}
	}

	// Resolve severity. The curated GHSA label (set on the struct above) is
	// authoritative when present. Otherwise fall back to the CVSS-derived
	// bucket, and only when neither is available default to medium — erring
	// conservative so an unclassified CVE surfaces at the default floor rather
	// than being silently demoted below it.
	if f.Severity == "" && scored {
		f.Severity = severityFromCVSS(f.CVSSScore)
	}
	if f.Severity == "" {
		f.Severity = SeverityMedium
	}
	return f
}

// osvResponse is the JSON shape returned by POST /v1/query.
type osvResponse struct {
	Vulns []osvVulnerability `json:"vulns"`
}

type osvVulnerability struct {
	ID               string        `json:"id"`
	Summary          string        `json:"summary"`
	Details          string        `json:"details"`
	Aliases          []string      `json:"aliases"`
	Severity         []osvSeverity `json:"severity"`
	References       []osvRef      `json:"references"`
	DatabaseSpecific struct {
		Severity string `json:"severity"`
	} `json:"database_specific"`
}

type osvSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"` // CVSS vector string
}

type osvRef struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

func queryOSV(client *http.Client, url, purl string) (*osvResponse, error) {
	body, err := json.Marshal(map[string]any{
		"package": map[string]string{"purl": purl},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode audit request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to build audit request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		// A transport failure (DNS, connection reset, timeout) is transient.
		return nil, &transientError{err: fmt.Errorf("OSV.dev request failed: %w", err)}
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &transientError{err: fmt.Errorf("failed to read OSV.dev response: %w", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		httpErr := fmt.Errorf("OSV.dev returned HTTP %d for %s: %s",
			resp.StatusCode, purl, strings.TrimSpace(string(data)))
		// 429 (rate limit) and 5xx (server-side) are worth retrying; other
		// non-2xx (e.g. 400 for a malformed PURL) are permanent.
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &transientError{
				err:        httpErr,
				retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			}
		}
		return nil, httpErr
	}
	var out osvResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("invalid OSV.dev response: %w", err)
	}
	return &out, nil
}
