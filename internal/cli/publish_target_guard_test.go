package cli

import (
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// GIS-600: `fglpkg publish` with no --registry, no FGLPKG_PUBLISH_REGISTRY and
// no defaultRegistry used to submit to the PUBLIC GI registry silently. That is
// the state right after installing fglpkg, so the documented bare `publish`
// uploaded internal code to a registry shared by every partner — and a
// submitted version cannot be withdrawn from the command line.
//
// These tests pin the guard: the fallback still exists, but it is never taken
// without the user saying so.

func testManifest() *manifest.Manifest {
	return manifest.New("acme-widget", "1.0.0", "", "")
}

// forceTTY makes the guard take its interactive branch.
func forceTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := stdinIsTerminal
	stdinIsTerminal = func() bool { return tty }
	t.Cleanup(func() { stdinIsTerminal = orig })
}

// A named target is a target the user chose, so nothing is confirmed. This is
// the case that must stay fast and silent, or every Artifactory publish grows a
// prompt.
func TestConfirmPublishTargetAcceptsANamedRegistry(t *testing.T) {
	forceTTY(t, false) // would refuse if the guard fired at all
	for _, name := range []string{"gi", "acme", "corp-artifactory"} {
		if err := confirmPublishTarget(publishFlags{registry: name}, testManifest()); err != nil {
			t.Errorf("--registry %s should need no confirmation, got: %v", name, err)
		}
	}
}

// --ci is the pipeline mode: there is nobody to ask, so the only safe answer is
// to refuse. The message has to carry the way out, or the pipeline is simply
// broken with no clue.
func TestConfirmPublishTargetRefusesUnderCI(t *testing.T) {
	forceTTY(t, true) // a terminal must not rescue --ci
	err := confirmPublishTarget(publishFlags{ci: true}, testManifest())
	if err == nil {
		t.Fatal("--ci with no configured target must refuse, not fall back to the public registry")
	}
	for _, want := range []string{
		"service.generointelligence.ai", // names where it would have gone
		"--registry gi",                 // the explicit opt-in
		"registry list",                 // how to find the alternatives
		"--publish-default",             // how to stop being asked
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal should mention %q, got:\n%s", want, err.Error())
		}
	}
}

// A pipe or a cron job is the same situation as --ci without the flag: the
// prompt would block or read EOF, and an EOF must never read as consent.
func TestConfirmPublishTargetRefusesWithoutATerminal(t *testing.T) {
	forceTTY(t, false)
	err := confirmPublishTarget(publishFlags{}, testManifest())
	if err == nil {
		t.Fatal("no terminal and no configured target must refuse")
	}
	if !strings.Contains(err.Error(), "not an interactive terminal") {
		t.Errorf("refusal should say why it cannot ask, got:\n%s", err.Error())
	}
}

// Declining uploads nothing. The distinction that matters is that this is an
// error (publish stops) rather than a warning it proceeds past.
func TestConfirmPublishTargetDeclinedStopsThePublish(t *testing.T) {
	forceTTY(t, true)
	withStdin(t, "n\n")
	out, err := captureStdout(t, func() error {
		return confirmPublishTarget(publishFlags{}, testManifest())
	})
	if err == nil {
		t.Fatal("answering no must stop the publish")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("want a cancellation message, got: %v", err)
	}
	// The question has to name the package and the destination, or "Publish
	// there?" is unanswerable.
	for _, want := range []string{"acme-widget@1.0.0", "service.generointelligence.ai", "PUBLIC"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt should state %q, got:\n%s", want, out)
		}
	}
}

// A bare Enter must not publish: the default is no.
func TestConfirmPublishTargetDefaultsToNo(t *testing.T) {
	forceTTY(t, true)
	withStdin(t, "\n")
	_, err := captureStdout(t, func() error {
		return confirmPublishTarget(publishFlags{}, testManifest())
	})
	if err == nil {
		t.Fatal("a bare Enter must not be read as consent to publish publicly")
	}
}

// An interrupted or closed stdin must not be read as a yes either. promptYesNo
// guarantees this; the test pins it through the guard, which is where it counts.
func TestConfirmPublishTargetClosedStdinIsNotConsent(t *testing.T) {
	forceTTY(t, true)
	withStdin(t, "") // immediate EOF
	_, err := captureStdout(t, func() error {
		return confirmPublishTarget(publishFlags{}, testManifest())
	})
	if err == nil {
		t.Fatal("EOF on stdin must not be read as consent")
	}
}

// Saying yes proceeds. The fallback is still reachable — the point of the guard
// is consent, not removal.
func TestConfirmPublishTargetAcceptedProceeds(t *testing.T) {
	forceTTY(t, true)
	withStdin(t, "y\n")
	_, err := captureStdout(t, func() error {
		return confirmPublishTarget(publishFlags{}, testManifest())
	})
	if err != nil {
		t.Fatalf("answering yes should proceed, got: %v", err)
	}
}

// --dry-run uploads nothing by definition, so blocking it would break pipelines
// that use it to validate a manifest while fixing nothing. It warns instead.
func TestConfirmPublishTargetDryRunIsNotBlocked(t *testing.T) {
	forceTTY(t, false) // the case that would otherwise refuse
	if err := confirmPublishTarget(publishFlags{dryRun: true}, testManifest()); err != nil {
		t.Fatalf("--dry-run must not be blocked, got: %v", err)
	}
	// Belt and braces: --ci --dry-run is a common pipeline combination.
	if err := confirmPublishTarget(publishFlags{dryRun: true, ci: true}, testManifest()); err != nil {
		t.Fatalf("--ci --dry-run must not be blocked, got: %v", err)
	}
}

// FGLPKG_REGISTRY repoints what `gi` means. An on-premise GI registry is a
// target the user has already chosen, and it is not the public service, so the
// guard must stand down — otherwise every publish against a private GI server
// needs --registry gi for no safety gain. Caught by the functional suite, whose
// mock registry is pointed at exactly this way.
func TestConfirmPublishTargetSkipsARepointedRegistry(t *testing.T) {
	t.Setenv("FGLPKG_REGISTRY", "https://gi.internal.acme.example")
	forceTTY(t, false) // the case that refuses when the target IS the public one
	if err := confirmPublishTarget(publishFlags{}, testManifest()); err != nil {
		t.Fatalf("a repointed FGLPKG_REGISTRY is not the public registry; got: %v", err)
	}
	if err := confirmPublishTarget(publishFlags{ci: true}, testManifest()); err != nil {
		t.Fatalf("--ci against a repointed registry should proceed; got: %v", err)
	}
}

// ...but FGLPKG_REGISTRY set to the public URL is still the public registry, so
// the guard must not be defeated by spelling the default out.
func TestConfirmPublishTargetStillGuardsTheExplicitPublicURL(t *testing.T) {
	t.Setenv("FGLPKG_REGISTRY", "https://service.generointelligence.ai")
	forceTTY(t, false)
	if err := confirmPublishTarget(publishFlags{}, testManifest()); err == nil {
		t.Fatal("naming the public registry via FGLPKG_REGISTRY must still be guarded")
	}
}

func TestIsHTTPURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://github.com/owner/repo", true},
		{"http://example.com/x", true},
		{`C:\work\git\repository\test.git`, false}, // the value from the GIS-600 report
		{"/home/me/src/widget", false},
		{"git@github.com:owner/repo.git", false},
		{"github.com/owner/repo", false}, // no scheme
		{"https://", false},              // scheme but no host
		{"", false},
	}
	for _, c := range cases {
		if got := isHTTPURL(c.in); got != c.want {
			t.Errorf("isHTTPURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// --publish-default is the way out of being asked (GIS-600): it records the new
// repository as the publish target, so `publish` stops falling back to the
// public GI registry. It mirrors --consume-default, including writing into the
// same file the descriptor landed in.
func TestCmdRegistryAdd_PublishDefaultProject(t *testing.T) {
	chdirTemp(t)
	if err := manifest.New("app", "1.0.0", "", "").Save("."); err != nil {
		t.Fatalf("save manifest: %v", err)
	}

	if err := cmdRegistryAdd([]string{
		"acme", "https://a.example", "--repo-key", "K", "--project", "--publish-default",
	}); err != nil {
		t.Fatalf("registry add --project --publish-default: %v", err)
	}

	got, err := manifest.Load(".")
	if err != nil {
		t.Fatalf("reload manifest: %v", err)
	}
	if got.DefaultRegistry != "acme" {
		t.Fatalf("defaultRegistry = %q, want acme", got.DefaultRegistry)
	}
	// The consume default is a separate knob and must be untouched.
	if got.DefaultConsumeRegistry != "" {
		t.Fatalf("defaultConsumeRegistry should stay empty, got %q", got.DefaultConsumeRegistry)
	}
}

func TestCmdRegistryAdd_PublishDefaultGlobal(t *testing.T) {
	home := chdirTemp(t)

	if err := cmdRegistryAdd([]string{
		"acme", "https://a.example", "--repo-key", "K", "--publish-default",
	}); err != nil {
		t.Fatalf("registry add --publish-default: %v", err)
	}

	g, err := config.LoadGlobalFile(home)
	if err != nil {
		t.Fatalf("load global: %v", err)
	}
	if g.DefaultRegistry != "acme" {
		t.Fatalf("defaultRegistry = %q, want acme", g.DefaultRegistry)
	}
	if g.DefaultConsumeRegistry != "" {
		t.Fatalf("defaultConsumeRegistry should stay empty, got %q", g.DefaultConsumeRegistry)
	}
}

// Both flags together is a legitimate combination: one repository serving as
// the only target a team uses.
func TestCmdRegistryAdd_BothDefaultsTogether(t *testing.T) {
	home := chdirTemp(t)

	if err := cmdRegistryAdd([]string{
		"acme", "https://a.example", "--repo-key", "K", "--consume-default", "--publish-default",
	}); err != nil {
		t.Fatalf("registry add with both defaults: %v", err)
	}

	g, _ := config.LoadGlobalFile(home)
	if g.DefaultRegistry != "acme" || g.DefaultConsumeRegistry != "acme" {
		t.Fatalf("want both defaults set to acme, got publish=%q consume=%q",
			g.DefaultRegistry, g.DefaultConsumeRegistry)
	}
}

// Adding a repository without the flag must not silently rewire where packages
// are published — the same guarantee --consume-default has.
func TestCmdRegistryAdd_WithoutPublishDefaultWritesNothing(t *testing.T) {
	home := chdirTemp(t)

	if err := cmdRegistryAdd([]string{"acme", "https://a.example", "--repo-key", "K"}); err != nil {
		t.Fatalf("registry add: %v", err)
	}

	g, _ := config.LoadGlobalFile(home)
	if g.DefaultRegistry != "" {
		t.Fatalf("defaultRegistry should stay empty, got %q", g.DefaultRegistry)
	}
}
