// Package config models fglpkg's package-repository configuration: the
// priority-ordered set of registries fglpkg consults for FGL/BDL packages.
//
// A repository is described by a secrets-free descriptor (see Registry). The
// effective set is a cascade, in increasing precedence (later wins per name):
//
//  1. the built-in Genero Intelligence (GI) registry — always present;
//  2. a machine-wide ~/.fglpkg/config.json ({"registries": [...]});
//  3. the project's fglpkg.json "registries" array.
//
// Credentials never live here — they stay in ~/.fglpkg/credentials.json, keyed
// by the repository URL. This mirrors Maven's pom.xml <repositories> + user
// settings.xml split.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/atomicfile"
)

// Registry describes one package repository. It carries no secrets.
type Registry struct {
	Name     string   `json:"name"`               // logical id; used in --registry, lock, credentials key
	Type     string   `json:"type"`               // "genero" | "artifactory"
	URL      string   `json:"url"`                // base URL (incl. any context path)
	RepoKey  string   `json:"repoKey,omitempty"`  // Artifactory generic-repo key; required for type=artifactory
	Priority int      `json:"priority,omitempty"` // lower = tried first; must be unique across the set
	Auth     string   `json:"auth,omitempty"`     // bearer|basic|apikey|anonymous (default bearer)
	Packages []string `json:"packages,omitempty"` // optional name-scope glob allow-list
}

// Repository types.
const (
	TypeGenero      = "genero"
	TypeArtifactory = "artifactory"
)

// Auth schemes.
const (
	AuthBearer    = "bearer"
	AuthBasic     = "basic"
	AuthAPIKey    = "apikey"
	AuthAnonymous = "anonymous"
)

// GIName is the logical name of the built-in Genero Intelligence registry.
const GIName = "gi"

// defaultGIURL mirrors registry.defaultRegistryBase — the hardcoded GI base.
const defaultGIURL = "https://service.generointelligence.ai"

// GlobalFilename is the machine-wide config file name under the fglpkg home.
const GlobalFilename = "config.json"

// DefaultMavenBase is the public Maven Central base used for JAR downloads when
// no Maven mirror is configured. Keeping it here (rather than only inline in
// manifest.MavenURL) lets the mirror resolver fall back to a single source.
const DefaultMavenBase = "https://repo1.maven.org/maven2"

// MavenMirror describes a Maven repository (typically a JFrog Artifactory
// Maven remote/virtual repo) that replaces Maven Central as the source for JAR
// downloads. It carries no secrets — credentials live in credentials.json,
// keyed by URL. URL is the base serving the standard Maven2 layout (e.g.
// https://acme.jfrog.io/artifactory/libs-release). Auth is the scheme used
// against it (bearer|basic|apikey|anonymous); empty defaults to bearer.
type MavenMirror struct {
	URL  string `json:"url"`
	Auth string `json:"auth,omitempty"`
}

// BuiltinGI returns the always-present GI registry descriptor. When
// fglpkgRegistry is non-empty (the FGLPKG_REGISTRY env override) it retargets
// the GI URL, so existing single-registry users are unaffected.
func BuiltinGI(fglpkgRegistry string) Registry {
	url := defaultGIURL
	if fglpkgRegistry != "" {
		url = strings.TrimRight(fglpkgRegistry, "/")
	}
	return Registry{Name: GIName, Type: TypeGenero, URL: url, Priority: 1, Auth: AuthBearer}
}

// LoadGlobal reads {home}/config.json and returns its registries. A missing
// file is not an error (returns nil). Unknown fields are rejected to catch typos.
func LoadGlobal(home string) ([]Registry, error) {
	g, err := loadGlobalFile(home)
	return g.Registries, err
}

// GlobalFile is the parsed shape of ~/.fglpkg/config.json.
type GlobalFile struct {
	Registries      []Registry `json:"registries"`
	DefaultRegistry string     `json:"defaultRegistry"` // logical name of the default publish target

	// DefaultConsumeRegistry names the repository the consuming commands
	// (install/update/search/info/outdated) resolve against when no --registry
	// flag is given (GIS-364). Machine-wide fallback; the project fglpkg.json and
	// FGLPKG_CONSUME_REGISTRY take precedence. "" => consult every configured
	// repository, as before.
	//
	// This is *exclusion*, not precedence: the named repository is the only one
	// consulted for an unpinned name, so it never silently picks between two
	// repositories. Carries omitempty so the read-modify-write cycle in `registry
	// add`/`remove` cannot inject "defaultConsumeRegistry": "" into a config that
	// never set it.
	DefaultConsumeRegistry string `json:"defaultConsumeRegistry,omitempty"`

	// MavenMirror, when set, reroutes JAR downloads from Maven Central to the
	// given Maven repository (GIS-365). Machine-wide fallback; the project
	// fglpkg.json and FGLPKG_MAVEN_URL take precedence. nil => Maven Central.
	MavenMirror *MavenMirror `json:"mavenMirror,omitempty"`

	// Passive update-check user settings (GIS-255). These are READ-ONLY here —
	// the tool never rewrites config.json. The mutable cache (last check time,
	// last seen version) lives in the separate tool-managed update-check.json
	// (see internal/updatecheck), so an advisory feature never reformats the
	// user's hand-edited registry config.
	//
	// Both carry omitempty so the read-modify-write cycle in `registry
	// add`/`remove` cannot inject "updateCheck": null / "updateCheckInterval": ""
	// into a config that never set them. A user's explicit false still
	// round-trips: the pointer is non-nil, so omitempty keeps it.
	UpdateCheck         *bool  `json:"updateCheck,omitempty"`         // opt-out; nil => default (enabled)
	UpdateCheckInterval string `json:"updateCheckInterval,omitempty"` // Go duration; "" => default 24h

	// Signing carries the signature-enforcement policy. The README "Signing"
	// section and this PR's policy-key hint both direct users to set
	// signing.enforce here, so the loader must accept it — loadGlobalFile decodes
	// with DisallowUnknownFields, and without this field that exact config.json
	// is rejected, which (worst case) makes the consuming path silently drop
	// every configured registry. Enforcement is resolved authoritatively by
	// internal/signing.EnforceMode via its own tolerant decoder; this field
	// exists only so the strict loader tolerates the key. omitempty keeps the
	// `registry add`/`remove` read-modify-write cycle from injecting an empty
	// "signing" block into a config that never set one — same reasoning as
	// updateCheck / mavenMirror above.
	Signing *SigningPolicy `json:"signing,omitempty"`
}

// SigningPolicy mirrors the shape internal/signing reads from config.json's
// "signing" object. It is intentionally read-only here: config never resolves
// enforcement from it — internal/signing.EnforceMode does — so only the fields
// the strict loader must tolerate are declared.
type SigningPolicy struct {
	Enforce string `json:"enforce,omitempty"`
}

// UpdateSettings are the resolved passive-update-check preferences.
type UpdateSettings struct {
	Enabled  bool
	Interval time.Duration
}

// DefaultUpdateInterval is the throttle window when config.json sets none.
const DefaultUpdateInterval = 24 * time.Hour

// LoadUpdateSettings reads the update-check preferences from config.json,
// applying defaults (enabled, 24h). A missing file or unset fields yield the
// defaults; an unparseable/zero interval falls back to the default rather than
// failing a command over an advisory feature. The returned settings are always
// usable even when err is non-nil (caller may simply skip the check).
func LoadUpdateSettings(home string) (UpdateSettings, error) {
	s := UpdateSettings{Enabled: true, Interval: DefaultUpdateInterval}
	g, err := loadGlobalFile(home)
	if err != nil {
		return s, err
	}
	if g.UpdateCheck != nil {
		s.Enabled = *g.UpdateCheck
	}
	if g.UpdateCheckInterval != "" {
		if d, perr := time.ParseDuration(g.UpdateCheckInterval); perr == nil && d > 0 {
			s.Interval = d
		}
	}
	return s, nil
}

// loadGlobalFile reads and parses the global config file. A missing or
// blank/whitespace-only file yields a zero GlobalFile (not an error). Unknown
// fields are rejected to catch typos.
func loadGlobalFile(home string) (GlobalFile, error) {
	p := filepath.Join(home, GlobalFilename)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return GlobalFile{}, nil
		}
		return GlobalFile{}, err
	}
	// A blank or whitespace-only file is treated as "no config", the same as a
	// missing file — an empty file is morally identical to absence, and an
	// editor leaving a 0-byte file should not hard-fail every command.
	if len(bytes.TrimSpace(data)) == 0 {
		return GlobalFile{}, nil
	}
	var f GlobalFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return GlobalFile{}, fmt.Errorf("invalid %s: %w", p, err)
	}
	return f, nil
}

// LoadGlobalFile reads and parses ~/.fglpkg/config.json. A missing or blank
// file yields a zero GlobalFile (not an error). It is the read half of the
// read-modify-write cycle used by `fglpkg registry add/remove`.
func LoadGlobalFile(home string) (GlobalFile, error) {
	return loadGlobalFile(home)
}

// WriteGlobalFile writes g to ~/.fglpkg/config.json as formatted JSON, creating
// the home directory if needed. It is the write half of `registry add/remove`.
func WriteGlobalFile(home string, g GlobalFile) error {
	if err := os.MkdirAll(home, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(filepath.Join(home, GlobalFilename), append(data, '\n'), 0644)
}

// GlobalDefaultRegistry returns the defaultRegistry declared in the global
// config file, or "" if none (or no file). Errors mirror LoadGlobal.
func GlobalDefaultRegistry(home string) (string, error) {
	g, err := loadGlobalFile(home)
	return g.DefaultRegistry, err
}

// GlobalConsumeRegistry returns the defaultConsumeRegistry declared in the
// global config file, or "" if none (or no file). Errors mirror LoadGlobal.
func GlobalConsumeRegistry(home string) (string, error) {
	g, err := loadGlobalFile(home)
	return g.DefaultConsumeRegistry, err
}

// GlobalMavenMirror returns the mavenMirror declared in the global config file,
// or nil if none (or no file). Errors mirror LoadGlobal.
func GlobalMavenMirror(home string) (*MavenMirror, error) {
	g, err := loadGlobalFile(home)
	return g.MavenMirror, err
}

// ResolveScalar returns the effective value of a "replace-semantics" config key
// under the standard GIS-368 precedence — environment > local (project) > global
// (user) — or "" when none is set. A whitespace-only value counts as unset, so a
// blank higher layer never shadows a set lower one, and the returned value is
// trimmed. It is the single source of truth for scalar config precedence;
// collection keys (registries) merge via Resolve instead. Callers pass the
// already-read values so this package stays free of the manifest import.
func ResolveScalar(env, local, global string) string {
	for _, v := range []string{env, local, global} {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// Load resolves the effective registry set: built-in GI (honoring
// FGLPKG_REGISTRY) ⊕ the global ~/.fglpkg/config.json ⊕ the project's manifest
// registries. The project registries are passed in by the caller so this
// package never imports the manifest package (avoiding an import cycle).
func Load(home, fglpkgRegistry string, projectRegistries []Registry) ([]Registry, error) {
	global, err := LoadGlobal(home)
	if err != nil {
		return nil, err
	}
	return Resolve(BuiltinGI(fglpkgRegistry), global, projectRegistries)
}

// Resolve merges builtin ⊕ global ⊕ project (increasing precedence, later wins
// per name), normalises, validates, and returns the priority-sorted set. It
// errors on duplicate priorities, an unknown type/auth, or an artifactory entry
// missing repoKey.
func Resolve(builtin Registry, global, project []Registry) ([]Registry, error) {
	byName := map[string]Registry{}
	order := []string{}
	add := func(r Registry) {
		if _, ok := byName[r.Name]; !ok {
			order = append(order, r.Name)
		}
		byName[r.Name] = r
	}
	add(builtin)
	for _, r := range global {
		add(r)
	}
	for _, r := range project {
		add(r)
	}

	merged := make([]Registry, 0, len(order))
	for _, n := range order {
		merged = append(merged, byName[n])
	}

	seenPriority := map[int]string{}
	for i := range merged {
		r := &merged[i]
		r.URL = strings.TrimRight(r.URL, "/")
		if r.Auth == "" {
			r.Auth = AuthBearer
		}
		// A GI entry (built-in or a retarget) defaults to priority 1 so simply
		// retargeting its URL without restating priority stays valid.
		if r.Name == GIName && r.Priority == 0 {
			r.Priority = 1
		}
		if err := validate(*r); err != nil {
			return nil, err
		}
		if other, dup := seenPriority[r.Priority]; dup {
			return nil, fmt.Errorf(
				"registries %q and %q share priority %d; priorities must be unique",
				other, r.Name, r.Priority,
			)
		}
		seenPriority[r.Priority] = r.Name
	}

	sort.SliceStable(merged, func(i, j int) bool { return merged[i].Priority < merged[j].Priority })
	return merged, nil
}

func validate(r Registry) error {
	if r.Name == "" {
		return fmt.Errorf("registry entry is missing 'name'")
	}
	if r.URL == "" {
		return fmt.Errorf("registry %q is missing 'url'", r.Name)
	}
	if r.Priority < 1 {
		return fmt.Errorf("registry %q must set a positive 'priority' (lower = tried first)", r.Name)
	}
	switch r.Type {
	case TypeGenero:
		// Only the built-in GI registry may be type=genero: the Genero client
		// resolves its base from the process-global registryBase()/FGLPKG_REGISTRY,
		// so a per-instance URL on any other genero entry would be silently
		// ignored (dead config) and mis-attribute results to it. Point an
		// internal GI mirror via FGLPKG_REGISTRY, or use type=artifactory.
		// (GIS-249 C1)
		if r.Name != GIName {
			return fmt.Errorf(
				"registry %q has type %q but only the built-in %q registry may be type=genero; "+
					"use type=artifactory, or retarget GI via FGLPKG_REGISTRY",
				r.Name, TypeGenero, GIName,
			)
		}
	case TypeArtifactory:
		if r.RepoKey == "" {
			return fmt.Errorf("registry %q (type=artifactory) requires 'repoKey'", r.Name)
		}
	default:
		return fmt.Errorf(
			"registry %q has unknown type %q (expected %q or %q)",
			r.Name, r.Type, TypeGenero, TypeArtifactory,
		)
	}
	switch r.Auth {
	case AuthBearer, AuthBasic, AuthAPIKey, AuthAnonymous:
		// ok
	default:
		return fmt.Errorf(
			"registry %q has unknown auth %q (expected bearer|basic|apikey|anonymous)",
			r.Name, r.Auth,
		)
	}
	return nil
}

// Admits reports whether this registry may serve the given package name, per
// its optional 'packages' glob allow-list. An empty list admits every name.
func (r Registry) Admits(name string) bool {
	if len(r.Packages) == 0 {
		return true
	}
	for _, pat := range r.Packages {
		if ok, _ := path.Match(pat, name); ok {
			return true
		}
	}
	return false
}

// Find returns the registry with the given name, if present.
func Find(regs []Registry, name string) (Registry, bool) {
	for _, r := range regs {
		if r.Name == name {
			return r, true
		}
	}
	return Registry{}, false
}

// RoutingLint is one project-layer config diagnostic from LintProjectRouting: a
// field-tagged message the caller renders as an error or a warning.
type RoutingLint struct {
	Field   string
	Message string
	Warning bool // false = error
}

// LintProjectRouting validates a project's routing config — its registries and
// the defaults/mirror that reference them — in ISOLATION, deliberately without
// the user's global config, so a checked-in fglpkg.json can be checked for
// portability across machines (GIS-368).
//
// A malformed registry is an error (Load would reject it anyway); a default that
// names a registry this project does not itself declare is a warning, since it
// may legitimately be supplied by the user's global config on some machines but
// would not resolve on a fresh clone.
func LintProjectRouting(registries []Registry, defaultRegistry, defaultConsumeRegistry string, mirror *MavenMirror) []RoutingLint {
	var out []RoutingLint
	declared := map[string]bool{GIName: true} // the built-in GI always resolves
	// Reserve priority 1 for the built-in GI: Resolve adds it first at priority 1,
	// so a project registry that also claims priority 1 hard-fails load. Seeding it
	// here makes lint catch that collision instead of emitting a false OK.
	seenPriority := map[int]string{1: GIName}
	for _, r := range registries {
		rc := r // normalise a copy so the checks match load-time behaviour
		rc.URL = strings.TrimRight(rc.URL, "/")
		if rc.Auth == "" {
			rc.Auth = AuthBearer
		}
		if rc.Name == GIName {
			out = append(out, RoutingLint{Field: "registries", Message: fmt.Sprintf("%q is the built-in registry and cannot be redeclared", GIName)})
			continue
		}
		if err := validate(rc); err != nil {
			out = append(out, RoutingLint{Field: "registries", Message: err.Error()})
			continue
		}
		if other, dup := seenPriority[rc.Priority]; dup {
			out = append(out, RoutingLint{Field: "registries", Message: fmt.Sprintf(
				"registries %q and %q share priority %d; priorities must be unique", other, rc.Name, rc.Priority)})
		}
		seenPriority[rc.Priority] = rc.Name
		declared[rc.Name] = true
	}
	checkDefault := func(field, name string) {
		if name != "" && !declared[name] {
			out = append(out, RoutingLint{Field: field, Warning: true, Message: fmt.Sprintf(
				"%s %q is not declared in this project's registries; it resolves only if the user's global config defines it",
				field, name)})
		}
	}
	checkDefault("defaultRegistry", defaultRegistry)
	checkDefault("defaultConsumeRegistry", defaultConsumeRegistry)
	if mirror != nil {
		switch {
		case strings.TrimSpace(mirror.URL) == "":
			out = append(out, RoutingLint{Field: "mavenMirror", Warning: true, Message: "mavenMirror has no 'url'; it will be ignored"})
		case !isHTTPURL(mirror.URL):
			out = append(out, RoutingLint{Field: "mavenMirror", Warning: true, Message: fmt.Sprintf(
				"mavenMirror url %q should be an http(s) URL", strings.TrimSpace(mirror.URL))})
		}
		switch mirror.Auth {
		case "", AuthBearer, AuthBasic, AuthAPIKey, AuthAnonymous:
			// ok — "" defaults to bearer
		default:
			out = append(out, RoutingLint{Field: "mavenMirror", Message: fmt.Sprintf(
				"mavenMirror auth %q is unknown (expected bearer|basic|apikey|anonymous)", mirror.Auth)})
		}
	}
	return out
}

func isHTTPURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
