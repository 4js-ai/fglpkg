// Package selfupdate implements `fglpkg self-update` (GIS-255): download the
// latest stable release binary for this OS/arch, verify its authenticity (an
// Ed25519 signature chained to the pinned release root) AND its integrity
// (SHA-256), then atomically replace the running executable.
//
// Authenticity is gated before anything is installed: a bad or missing
// signature aborts BEFORE the binary is downloaded, and a checksum mismatch
// aborts before the swap. Every abort returns an error whose message carries the
// GI-served manual-download recovery path; the caller prints it and exits
// non-zero. Scope is latest-stable only — no pinning, pre-release, or downgrade.
package selfupdate

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/checksum"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	"github.com/4js-mikefolcher/fglpkg/internal/signing"
	"github.com/4js-mikefolcher/fglpkg/internal/updatecheck"
)

// Options configures a self-update run.
type Options struct {
	Current      string                   // the running version (cli.Version)
	Check        bool                     // report availability and exit; never writes
	Yes          bool                     // skip the confirmation prompt
	Force        bool                     // re-install even if already latest
	Stdout       io.Writer                // progress/success output
	Confirm      func(prompt string) bool // interactive confirm; required unless Yes/Check
	HomeForCache string                   // fglpkg home; refreshes the update-check cache on success

	// Test seams — unexported, so the production API stays clean and callers
	// always get the live behavior. Internal tests set these.
	exePath string
	roots   []signing.Root
	now     time.Time
}

// Run performs the self-update flow. On success it prints to opts.Stdout and
// returns nil. On any failure it returns an error whose message is the
// user-facing guidance (including the recovery path for download/verify aborts);
// the caller prints that verbatim and exits non-zero.
func Run(opts Options) error {
	// 1. Guard: only released builds installed as a plain writable binary.
	if opts.Current == "" || opts.Current == "dev" {
		return errors.New("self-update is only available for released builds (this is a 'dev' build); install a tagged release binary")
	}
	exePath := opts.exePath
	if exePath == "" {
		p, err := resolveExe()
		if err != nil {
			return fmt.Errorf("cannot locate the running executable: %w", err)
		}
		exePath = p
	}
	cleanupStaleWindowsBackup(exePath)
	if mgr := managedBy(exePath); mgr != "" {
		return fmt.Errorf("fglpkg looks installed via %s (%s); update it with that tool instead of self-update", mgr, exePath)
	}

	// 2. Resolve the latest release.
	lr, err := registry.FetchLatestFGLPkg()
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return errors.New("this registry does not provide fglpkg release information yet")
		}
		return fmt.Errorf("could not check for updates: %w", err)
	}
	// Scope is latest-stable only. The endpoint contract says `version` is the
	// latest STABLE release, but enforce it client-side too: if the server ever
	// serves an rc, semver ordering would rank it newer and self-update would
	// happily install it. Checked BEFORE --check and --force so neither can slip
	// a pre-release past the scope. Advisory, not an error — the tool is working
	// as designed, there is simply nothing installable.
	lv, lerr := semver.Parse(lr.Version)
	switch {
	case lerr != nil:
		fmt.Fprintf(opts.Stdout, "The registry reported %q as the latest version, which is not a valid release number; nothing to install.\n", lr.Version)
		fmt.Fprintf(opts.Stdout, "fglpkg is up to date (v%s)\n", opts.Current)
		return nil
	case lv.PreRelease != "":
		fmt.Fprintf(opts.Stdout, "The registry's latest release (%s) is a pre-release; self-update installs stable releases only.\n", lr.Version)
		fmt.Fprintf(opts.Stdout, "fglpkg is up to date (v%s)\n", opts.Current)
		return nil
	}
	isNewer := versionNewer(opts.Current, lr.Version)
	if opts.Check {
		if isNewer {
			fmt.Fprintf(opts.Stdout, "A new fglpkg is available: %s → %s\n", opts.Current, lr.Version)
		} else {
			fmt.Fprintf(opts.Stdout, "fglpkg is up to date (v%s)\n", opts.Current)
		}
		return nil
	}
	if !isNewer && !opts.Force {
		fmt.Fprintf(opts.Stdout, "fglpkg is up to date (v%s)\n", opts.Current)
		return nil
	}

	// 3. Select the asset for this platform.
	asset := lr.AssetFor(runtime.GOOS, runtime.GOARCH)
	if asset == nil {
		return recoveryErr(lr, fmt.Sprintf("no fglpkg %s binary is published for %s/%s", lr.Version, runtime.GOOS, runtime.GOARCH))
	}

	// 4. Fetch and AUTHENTICATE checksums before downloading the binary.
	if lr.ChecksumsURL == "" || lr.ChecksumsSigURL == "" {
		return recoveryErr(lr, "the release does not publish a signed checksums file")
	}
	checksums, err := fetchURL(lr.ChecksumsURL)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not fetch checksums.txt: %v", err))
	}
	sig, err := fetchURL(lr.ChecksumsSigURL)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not fetch the release signature: %v", err))
	}
	keysURL := lr.KeysManifestURL()
	if keysURL == "" {
		return recoveryErr(lr, "the release does not publish a signing-key manifest")
	}
	keysJSON, err := fetchURL(keysURL)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not fetch the signing-key manifest: %v", err))
	}
	roots := opts.roots
	if roots == nil {
		roots = signing.PinnedRoots()
	}
	now := opts.now
	if now.IsZero() {
		now = time.Now()
	}
	vc, err := authenticateChecksums(checksums, sig, keysJSON, roots, now)
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("release authenticity check failed: %v", err))
	}
	assetName := filepath.Base(asset.URL)
	expectedSHA, ok := vc.sha(assetName)
	if !ok {
		return recoveryErr(lr, fmt.Sprintf("the signed checksums have no entry for %s", assetName))
	}

	// 5. Confirm.
	if !opts.Yes {
		prompt := fmt.Sprintf("Update fglpkg v%s → v%s?", opts.Current, lr.Version)
		if opts.Confirm == nil || !opts.Confirm(prompt) {
			fmt.Fprintln(opts.Stdout, "Update cancelled.")
			return nil
		}
	}

	// 6. Download to a temp file in the target directory (same filesystem, so
	//    the final rename is atomic).
	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".fglpkg-update-*")
	if err != nil {
		return recoveryErr(lr, fmt.Sprintf("cannot write to %s (insufficient permissions?): %v", dir, err))
	}
	staged := tmp.Name()
	defer os.Remove(staged) // no-op once renamed into place
	fmt.Fprintf(opts.Stdout, "Downloading fglpkg v%s for %s/%s…\n", lr.Version, runtime.GOOS, runtime.GOARCH)
	if err := downloadTo(tmp, asset.URL); err != nil {
		tmp.Close()
		return recoveryErr(lr, fmt.Sprintf("download failed: %v", err))
	}
	tmp.Close()

	// 7. Integrity gate: verify the download against the authenticated SHA-256.
	if err := checksum.VerifyFile(staged, expectedSHA); err != nil {
		return recoveryErr(lr, fmt.Sprintf("downloaded binary failed checksum verification: %v", err))
	}

	// 8. Swap atomically, preserving the executable bit.
	if err := applyMode(staged, exePath); err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not set permissions on the new binary: %v", err))
	}
	if err := atomicReplace(exePath, staged); err != nil {
		return recoveryErr(lr, fmt.Sprintf("could not replace %s (insufficient permissions?): %v", exePath, err))
	}

	// 9. Done. Refresh the update-check cache so the passive notice goes quiet.
	fmt.Fprintf(opts.Stdout, "Updated fglpkg v%s → v%s\n", opts.Current, lr.Version)
	if opts.HomeForCache != "" {
		_ = updatecheck.SaveState(opts.HomeForCache, updatecheck.State{LastCheck: now, LatestKnown: lr.Version})
	}
	return nil
}

// resolveExe returns the absolute path of the running executable with symlinks
// resolved, so we replace the real file rather than a symlink into it.
func resolveExe() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}
	return p, nil
}

// managedBy returns a best-effort package-manager name if exePath looks owned by
// one (Homebrew, Linuxbrew), else "". Conservative: when unsure it returns ""
// and lets the atomic-write step fail cleanly instead.
func managedBy(exePath string) string {
	lower := strings.ToLower(filepath.ToSlash(exePath))
	switch {
	case strings.Contains(lower, "/cellar/"), strings.Contains(lower, "/homebrew/"), strings.Contains(lower, "/.linuxbrew/"):
		return "Homebrew"
	}
	return ""
}

// versionNewer reports whether latest is a newer STABLE release than current.
// Unparseable versions are treated as not newer (fail safe), and so is a
// pre-release: latest-stable-only is the documented scope, so an rc is never an
// upgrade target no matter how semver orders it.
func versionNewer(current, latest string) bool {
	c, err1 := semver.Parse(current)
	l, err2 := semver.Parse(latest)
	if err1 != nil || err2 != nil {
		return false
	}
	if l.PreRelease != "" {
		return false
	}
	return l.GreaterThan(c)
}

// recoveryErr builds an error whose message carries the GI-served manual-download
// recovery path verbatim, appended to msg. The caller prints it and exits.
func recoveryErr(lr *registry.LatestRelease, msg string) error {
	var b strings.Builder
	b.WriteString(msg)
	if lr != nil && lr.ManualURL != "" {
		fmt.Fprintf(&b, "\nDownload manually: %s", lr.ManualURL)
	}
	if lr != nil && lr.Instructions != "" {
		fmt.Fprintf(&b, "\n%s", lr.Instructions)
	}
	return errors.New(b.String())
}

// Bounds on the network step. self-update is a foreground, security-sensitive
// command fetching attacker-influenceable *content* (not trust anchors), so a
// slow or hostile server must not be able to hang it forever or hand it an
// unbounded body. Vars, not consts, so tests can shrink them.
var (
	// metaTimeout covers a whole metadata exchange: checksums.txt, its
	// signature, and keys.json are all a few KB, so 30s is generous.
	metaTimeout = 30 * time.Second
	// downloadTimeout bounds the binary download. Generous — a ~20 MB binary on
	// a poor link is legitimate — but not unbounded.
	downloadTimeout = 15 * time.Minute
	// maxMetaBytes caps the small signed-metadata reads.
	maxMetaBytes int64 = 1 << 20 // 1 MiB
	// maxBinaryBytes caps the download so a broken or hostile server cannot fill
	// the disk. Well above any real fglpkg binary; overshoot would fail the
	// SHA-256 gate anyway, but the disk should never get there.
	maxBinaryBytes int64 = 512 << 20 // 512 MiB
)

// newTransport returns a transport with per-phase timeouts, so a stalled dial,
// handshake, or missing response header fails fast independently of the overall
// client deadline. ForceAttemptHTTP2 is required because setting DialContext
// disables the automatic HTTP/2 upgrade http.DefaultTransport would have done.
func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// fetchURL GETs url and returns the body, erroring on a non-2xx status. Used for
// release assets on GitHub (not registry endpoints), so it is unauthenticated
// and follows redirects (the client default). The read is capped at
// maxMetaBytes: an oversized body is rejected rather than truncated, because a
// silently truncated checksums file deserves a clear error, not a confusing
// downstream signature failure.
func fetchURL(url string) ([]byte, error) {
	client := &http.Client{Timeout: metaTimeout, Transport: newTransport()}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetaBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxMetaBytes {
		return nil, fmt.Errorf("response exceeds the %d-byte limit for release metadata", maxMetaBytes)
	}
	return data, nil
}

// downloadTo streams url into w, bounded by downloadTimeout and maxBinaryBytes.
func downloadTo(w io.Writer, url string) error {
	client := &http.Client{Timeout: downloadTimeout, Transport: newTransport()}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, maxBinaryBytes+1))
	if err != nil {
		return err
	}
	if n > maxBinaryBytes {
		return fmt.Errorf("download exceeds the %d-byte limit", maxBinaryBytes)
	}
	return nil
}
