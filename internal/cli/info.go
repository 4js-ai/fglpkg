package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
	slugutil "github.com/4js-mikefolcher/fglpkg/internal/slug"
)

// cmdInfo fetches registry metadata for a package (or a specific version)
// and prints a human-readable summary. Use --json to emit the raw
// PackageInfo JSON instead, for piping into jq / scripts.
//
//	fglpkg info <name>                → latest version, pretty-printed
//	fglpkg info <name>@<version>      → specific version, pretty-printed
//	fglpkg info <name> --json         → raw PackageInfo JSON
func cmdInfo(args []string) error {
	var (
		jsonOut bool
		target  string
	)
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("unknown flag %q", a)
			}
			if target != "" {
				return fmt.Errorf("too many arguments: %q", a)
			}
			target = a
		}
	}
	if target == "" {
		return fmt.Errorf("usage: fglpkg info <package>[@<version>] [--json]")
	}

	name, version, err := parsePackageArg(target)
	if err != nil {
		return err
	}

	// Route through the multi-provider set when secondary repositories are
	// configured, so an Artifactory-sourced package is queried against its own
	// repository instead of GI (which would 404). Falls back to the GI-only
	// client otherwise (byte-identical legacy behaviour).
	home, _ := fglpkgHome()
	var m *manifest.Manifest
	if mm, mErr := manifest.Load("."); mErr == nil {
		m = mm
	}
	rs, _, _, rsErr := buildRepositorySet(home, m)
	if rsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring registries config: %v\n", rsErr)
	}
	// A configured consume default scopes the lookup to one repository (GIS-364),
	// so `info` reports on the copy `install` would actually resolve. `info` has
	// no --registry flag, so the default is the only scoping input.
	reg, fromDefault, err := applyConsumeRegistry(rs, home, m, "")
	if err != nil {
		return err
	}
	noteConsumeDefault(rs, reg, fromDefault)

	versions, err := infoVersionList(rs, name)
	// A package published by fglpkg <= 4.2.9 can sit in an Artifactory
	// repository under the name as its publisher typed it rather than under the
	// canonical slug every lookup now uses, so the canonical read misses it
	// (GIS-610). `info` describes a package without recording it anywhere, so
	// it may look under the typed spelling as well — after the canonical lookup
	// has failed, and only when the two spellings differ, because a name that
	// IS its own slug has already been tried.
	//
	// Nothing else does this: a package installed out of such a folder would be
	// written into fglpkg.json and the lock under its slug, where the next
	// install cannot find it. If the fallback finds nothing either, the error
	// reported is the canonical one, which names the repositories searched.
	legacyFolder := false
	if err != nil && rs != nil && errors.Is(err, registry.ErrNotFound) &&
		slugutil.Canonical(name) != name {
		if vs, vErr := rs.VersionsVerbatim(name); vErr == nil {
			versions, err, legacyFolder = candidateVersionList(vs), nil, true
		} else if !errors.Is(vErr, registry.ErrNotFound) {
			// A collision or an auth failure is an ANSWER, not an absence: the
			// folder is in two repositories and no scoping says which, or a
			// repository refused to say. Reporting the canonical "not found in
			// any configured repository" for either tells the reader the
			// opposite of what was measured, and 4.2.9 reported both.
			err = vErr
		}
	}
	if err != nil {
		hintReg := reg
		if hintReg == "" {
			hintReg = pinnedRegistry(m, name)
		}
		return privateHint(err, name, hintReg)
	}
	if len(versions.Versions) == 0 {
		return fmt.Errorf("package %q has no published versions", name)
	}

	// Resolve "latest" or any constraint-ish input to a concrete version.
	resolvedVersion := version
	if version == "" || version == "latest" || version == "*" {
		resolvedVersion = latestVersion(versions.Versions)
	}

	info, err := infoFetch(rs, name, resolvedVersion, legacyFolder)
	if err != nil {
		return err
	}
	if legacyFolder {
		warnLegacyFolder(name)
	}
	// Some registry payloads omit the name (it's in the URL). Fall back
	// to what the user asked for so the header and install hint render.
	if info.Name == "" {
		info.Name = name
	}
	if info.Version == "" {
		info.Version = resolvedVersion
	}

	if jsonOut {
		out, err := json.MarshalIndent(info, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	printInfo(info, versions, resolvedVersion == latestVersion(versions.Versions), legacyFolder)
	return nil
}

// infoVersionList lists a package's versions, routing through the
// multi-provider set when one is configured (rs != nil), else the GI client.
func infoVersionList(rs *provider.RepositorySet, name string) (*registry.VersionList, error) {
	if rs == nil {
		return registry.FetchVersionList(name)
	}
	cvs, err := rs.Versions(name)
	if err != nil {
		return nil, err
	}
	return candidateVersionList(cvs), nil
}

// candidateVersionList renders a provider's candidate list as the version list
// the rest of `info` reads.
func candidateVersionList(cvs []resolver.CandidateVersion) *registry.VersionList {
	vs := make([]string, 0, len(cvs))
	for _, cv := range cvs {
		vs = append(vs, cv.Version.String())
	}
	return &registry.VersionList{Versions: vs}
}

// warnLegacyFolder says that this package was described from a folder named
// before fglpkg canonicalized names, and that nothing but `info` can reach it.
// It goes to stderr so --json output stays a clean document.
func warnLegacyFolder(name string) {
	slug := slugutil.Canonical(name)
	fmt.Fprintf(os.Stderr,
		"warning: %q was found under that exact name, not under %q, so it was published by "+
			"fglpkg <= 4.2.9.\n"+
			"  `fglpkg install %s` cannot reach it. Re-publish it with fglpkg >= 4.2.10, or "+
			"rename the folder in the repository to %q.\n",
		name, slug, name, slug)
}

// infoFetch returns full metadata for name@version, routing through the
// multi-provider set when one is configured (rs != nil), else the GI client.
// legacyFolder asks for the package stored under exactly this name rather than
// under its slug; see the fallback in cmdInfo.
func infoFetch(rs *provider.RepositorySet, name, version string, legacyFolder bool) (*registry.PackageInfo, error) {
	if rs == nil {
		return registry.FetchInfo(name, version)
	}
	generoMajor := ""
	if gv, err := genero.Detect(); err == nil {
		generoMajor = gv.MajorString()
	}
	fetch := rs.Info
	if legacyFolder {
		fetch = rs.InfoVerbatim
	}
	info, err := fetch(name, version, generoMajor)
	// `info` describes a package; it does not install one. A version with no
	// build for this runtime is still worth describing — its Variants line says
	// which majors it serves — so fall back to the major-less lookup the
	// single-registry path always uses (GIS-574).
	var noBuild *registry.NoBuildError
	if errors.As(err, &noBuild) {
		return fetch(name, version, "")
	}
	return info, err
}

// latestVersion picks the newest entry from a version list using the
// project's semver ordering (so prereleases sort below their release).
// Falls back to lexical ordering for any strings that fail to parse.
func latestVersion(vs []string) string {
	if len(vs) == 0 {
		return ""
	}
	sorted := make([]string, len(vs))
	copy(sorted, vs)
	sort.Slice(sorted, func(i, j int) bool {
		av, aerr := semver.Parse(sorted[i])
		bv, berr := semver.Parse(sorted[j])
		switch {
		case aerr != nil && berr != nil:
			return sorted[i] < sorted[j]
		case aerr != nil:
			return true
		case berr != nil:
			return false
		}
		return av.LessThan(bv)
	})
	return sorted[len(sorted)-1]
}

// printInfo renders the human-readable summary. legacyFolder says the package
// was described from a folder named before fglpkg canonicalized names, which
// changes only the last line: there is no install command that reaches it.
func printInfo(info *registry.PackageInfo, versions *registry.VersionList, isLatest, legacyFolder bool) {
	header := fmt.Sprintf("%s@%s", info.Name, info.Version)
	if isLatest {
		header += " (latest)"
	}
	fmt.Println(header)
	fmt.Println(strings.Repeat("─", len(header)))
	fmt.Println()

	// npm-style deprecation block — placed right under the header so it's the
	// first thing seen. Advisory only; the version is still installable.
	if info.Deprecated {
		val := "yes"
		if info.DeprecationMessage != "" {
			val = "yes — " + info.DeprecationMessage
		}
		printField("Deprecated", val)
		printField("Moved to", info.MovedTo)
		fmt.Println()
	}

	printField("Description", info.Description)
	printField("Author", info.Author)
	// Source is the owning repository ("gi", "acme-internal"); populated only
	// when multi-provider routing is active, so single-registry output is
	// unchanged.
	printField("Source", info.Source)
	printField("License", info.License)
	printField("Genero", info.GeneroConstraint)
	printField("Published", info.PublishedAt)
	if info.Checksum != "" {
		printField("Checksum", "sha256:"+info.Checksum)
	}
	printField("Download", info.DownloadURL)

	if len(info.Variants) > 0 {
		majors := make([]string, 0, len(info.Variants))
		for _, v := range info.Variants {
			majors = append(majors, v.GeneroMajor)
		}
		printField("Variants", strings.Join(majors, ", "))
	}

	if len(info.FGLDeps) > 0 {
		fmt.Println("\nFGL dependencies:")
		names := make([]string, 0, len(info.FGLDeps))
		for n := range info.FGLDeps {
			names = append(names, n)
		}
		sort.Strings(names)
		width := longestName(names)
		for _, n := range names {
			fmt.Printf("  %-*s  %s\n", width, n, info.FGLDeps[n])
		}
	}

	if len(info.JavaDeps) > 0 {
		fmt.Println("\nJava dependencies:")
		for _, d := range info.JavaDeps {
			fmt.Printf("  %s:%s:%s\n", d.GroupID, d.ArtifactID, d.Version)
		}
	}

	if len(versions.Versions) > 0 {
		fmt.Printf("\nVersions (%d): %s\n", len(versions.Versions), strings.Join(versions.Versions, ", "))
	}

	if legacyFolder {
		// The warning that goes with this is on stderr, so that --json stays a
		// clean document; printing the usual hint here would contradict it on
		// stdout, and a reader who redirected stdout to a file would see only
		// the hint. Say it where the hint would have been instead (GIS-610).
		fmt.Printf("\nInstall: not possible — %q is stored under that exact name, not %q.\n"+
			"         Re-publish it with fglpkg >= 4.2.10, or rename the folder in the repository.\n",
			info.Name, slugutil.Canonical(info.Name))
		return
	}
	fmt.Printf("\nInstall: fglpkg install %s@%s\n", info.Name, info.Version)
}

func printField(label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(os.Stdout, "  %-12s %s\n", label+":", value)
}

func longestName(names []string) int {
	max := 0
	for _, n := range names {
		if len(n) > max {
			max = len(n)
		}
	}
	return max
}
