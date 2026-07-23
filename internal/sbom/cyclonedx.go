// Package sbom builds Software Bill of Materials documents from a
// project's fglpkg.lock. v1 emits CycloneDX 1.5 JSON only.
//
// The package performs no network I/O. All data flows from the
// lockfile through the Build() function into a Document value that
// callers marshal to JSON.
package sbom

import (
	"crypto/sha256"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// Spec constants for CycloneDX 1.5.
const (
	bomFormat   = "CycloneDX"
	specVersion = "1.5"

	defaultToolName   = "fglpkg"
	defaultToolVendor = "Four Js"
)

// Document is the CycloneDX 1.5 root object. JSON tags match the
// CycloneDX schema; fields that may be empty use omitempty so the
// output stays clean when data is unavailable.
type Document struct {
	BomFormat    string       `json:"bomFormat"`
	SpecVersion  string       `json:"specVersion"`
	SerialNumber string       `json:"serialNumber"`
	Version      int          `json:"version"`
	Metadata     Metadata     `json:"metadata"`
	Components   []Component  `json:"components,omitempty"`
	Dependencies []Dependency `json:"dependencies,omitempty"`
}

// Metadata describes who/when produced the BOM and the root component.
type Metadata struct {
	Timestamp string     `json:"timestamp"`
	Tools     []Tool     `json:"tools,omitempty"`
	Component *Component `json:"component,omitempty"`
}

// Tool identifies the tool that produced the BOM.
type Tool struct {
	Vendor  string `json:"vendor,omitempty"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Component is one software component (a library, application, etc.).
type Component struct {
	BomRef             string              `json:"bom-ref,omitempty"`
	Type               string              `json:"type"`
	Name               string              `json:"name"`
	Group              string              `json:"group,omitempty"`
	Version            string              `json:"version,omitempty"`
	PURL               string              `json:"purl,omitempty"`
	Hashes             []Hash              `json:"hashes,omitempty"`
	ExternalReferences []ExternalReference `json:"externalReferences,omitempty"`
	Properties         []Property          `json:"properties,omitempty"`
}

// Hash is a cryptographic digest of the component's artifact.
type Hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

// ExternalReference points to something outside the BOM (download URL,
// VCS, advisories, etc.). v1 emits only "distribution" entries.
type ExternalReference struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Property is a tool-specific key/value annotation. CycloneDX prescribes
// nothing about the semantics; we use it to carry Genero variant info.
type Property struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Dependency is one edge of the dependency graph. Ref points at a
// component (by bom-ref), DependsOn lists components it requires.
type Dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// Options configure SBOM generation. Zero values pick sensible defaults
// suitable for production use; tests inject Now and NewUUID for
// deterministic output.
type Options struct {
	Production  bool
	ToolName    string
	ToolVendor  string
	ToolVersion string
	Now         func() time.Time
	NewUUID     func() string
}

// rootRef is the bom-ref used for the project itself. CycloneDX does
// not prescribe a value; we use a stable string so dependency edges
// rooted at the project are recognizable across runs.
const rootRef = "root"

// Build constructs a CycloneDX Document from a lockfile and the given
// options. Build performs no I/O; callers marshal the returned value
// to JSON themselves.
func Build(lf *lockfile.LockFile, opts Options) *Document {
	now := opts.Now
	if now == nil {
		now = defaultNow
	}
	toolName := opts.ToolName
	if toolName == "" {
		toolName = defaultToolName
	}
	toolVendor := opts.ToolVendor
	if toolVendor == "" {
		toolVendor = defaultToolVendor
	}

	// Build BDL package components (sorted for stable output).
	pkgs := append([]lockfile.LockedPackage(nil), lf.Packages...)
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	var components []Component
	for _, p := range pkgs {
		components = append(components, bdlComponent(p))
	}

	// Build JAR components (sorted by key for stable output).
	jars := filterJARs(lf.JARs, opts.Production)
	sort.Slice(jars, func(i, j int) bool { return jars[i].Key < jars[j].Key })
	for _, j := range jars {
		components = append(components, jarComponent(j))
	}

	doc := &Document{
		BomFormat:   bomFormat,
		SpecVersion: specVersion,
		Version:     1,
		Metadata: Metadata{
			Timestamp: now().UTC().Format(time.RFC3339),
			Tools: []Tool{{
				Vendor:  toolVendor,
				Name:    toolName,
				Version: opts.ToolVersion,
			}},
			Component: &Component{
				BomRef:  rootRef,
				Type:    "application",
				Name:    lf.RootManifest.Name,
				Version: lf.RootManifest.Version,
			},
		},
		Components:   components,
		Dependencies: buildDependencyEdges(pkgs, jars),
	}

	// Serial number: derived from the document's content so the same lockfile
	// yields the same serial across runs (byte-reproducible). Tests may inject
	// a fixed UUID via opts.NewUUID.
	if opts.NewUUID != nil {
		doc.SerialNumber = "urn:uuid:" + opts.NewUUID()
	} else {
		doc.SerialNumber = "urn:uuid:" + deterministicUUID(bomSeed(doc))
	}
	return doc
}

// defaultNow returns the BOM timestamp. It honors SOURCE_DATE_EPOCH (the
// reproducible-builds convention): when set to a Unix timestamp, that instant
// is used so the whole document is byte-reproducible; otherwise the current
// time is used.
func defaultNow() time.Time {
	if s := strings.TrimSpace(os.Getenv("SOURCE_DATE_EPOCH")); s != "" {
		if epoch, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.Unix(epoch, 0).UTC()
		}
	}
	return time.Now().UTC()
}

// bomSeed builds a stable string capturing the document's identity and its
// component/dependency content (already emitted in sorted order), from which a
// deterministic serial number is derived.
func bomSeed(doc *Document) string {
	var b strings.Builder
	b.WriteString(doc.BomFormat)
	b.WriteByte('\n')
	b.WriteString(doc.SpecVersion)
	b.WriteByte('\n')
	if doc.Metadata.Component != nil {
		b.WriteString(doc.Metadata.Component.Name)
		b.WriteByte('@')
		b.WriteString(doc.Metadata.Component.Version)
	}
	b.WriteByte('\n')
	for _, c := range doc.Components {
		b.WriteString(c.PURL)
		b.WriteByte('|')
		b.WriteString(c.Version)
		for _, h := range c.Hashes {
			b.WriteByte('|')
			b.WriteString(h.Alg)
			b.WriteByte(':')
			b.WriteString(h.Content)
		}
		b.WriteByte('\n')
	}
	for _, d := range doc.Dependencies {
		b.WriteString(d.Ref)
		b.WriteString("->")
		b.WriteString(strings.Join(d.DependsOn, ","))
		b.WriteByte('\n')
	}
	return b.String()
}

// filterJARs drops dev-scoped JARs when production mode is on.
// optional-scoped JARs are always included.
func filterJARs(in []lockfile.LockedJAR, production bool) []lockfile.LockedJAR {
	if !production {
		return append([]lockfile.LockedJAR(nil), in...)
	}
	out := make([]lockfile.LockedJAR, 0, len(in))
	for _, j := range in {
		if j.Scope == "dev" {
			continue
		}
		out = append(out, j)
	}
	return out
}

func bdlComponent(p lockfile.LockedPackage) Component {
	purl := bdlPURL(p.Name, p.Version)
	c := Component{
		BomRef:  purl,
		Type:    "library",
		Name:    p.Name,
		Version: p.Version,
		PURL:    purl,
	}
	if p.Checksum != "" {
		c.Hashes = []Hash{{Alg: "SHA-256", Content: p.Checksum}}
	}
	if p.DownloadURL != "" {
		c.ExternalReferences = []ExternalReference{{
			Type: "distribution",
			URL:  p.DownloadURL,
		}}
	}
	if p.GeneroMajor != "" {
		c.Properties = []Property{{
			Name:  "fglpkg:generoMajor",
			Value: p.GeneroMajor,
		}}
	}
	return c
}

func jarComponent(j lockfile.LockedJAR) Component {
	purl := mavenPURL(j.GroupID, j.ArtifactID, j.Version)
	c := Component{
		BomRef:  purl,
		Type:    "library",
		Name:    j.ArtifactID,
		Group:   j.GroupID,
		Version: j.Version,
		PURL:    purl,
	}
	if j.Checksum != "" {
		c.Hashes = []Hash{{Alg: "SHA-256", Content: j.Checksum}}
	}
	if j.DownloadURL != "" {
		c.ExternalReferences = []ExternalReference{{
			Type: "distribution",
			URL:  j.DownloadURL,
		}}
	}
	return c
}

// buildDependencyEdges turns the lockfile's requiredBy fields into
// CycloneDX dependency entries. BDL packages carry requiredBy
// precisely; JARs do not yet (their parentage isn't in the lockfile),
// so we emit a single root → all-JARs edge until that gap is closed.
func buildDependencyEdges(pkgs []lockfile.LockedPackage, jars []lockfile.LockedJAR) []Dependency {
	// edges[ref] = ordered slice of children. Using maps lets multiple
	// requiredBy entries collapse cleanly into one edge per parent.
	edges := map[string][]string{}
	order := []string{}
	add := func(parent, child string) {
		if _, seen := edges[parent]; !seen {
			order = append(order, parent)
		}
		edges[parent] = append(edges[parent], child)
	}

	for _, p := range pkgs {
		child := bdlPURL(p.Name, p.Version)
		if len(p.RequiredBy) == 0 {
			// Stray entry with no parent — assume it's a direct dep.
			add(rootRef, child)
			continue
		}
		for _, parent := range p.RequiredBy {
			if parent == "<root>" {
				add(rootRef, child)
				continue
			}
			// Parent is another BDL package name. We need its version
			// to form a bom-ref; look it up.
			ver := findPkgVersion(pkgs, parent)
			if ver == "" {
				// Unknown parent — collapse onto root rather than emit
				// a dangling reference.
				add(rootRef, child)
				continue
			}
			add(bdlPURL(parent, ver), child)
		}
	}

	// JARs all hang off root for now (see spec open question).
	for _, j := range jars {
		add(rootRef, mavenPURL(j.GroupID, j.ArtifactID, j.Version))
	}

	out := make([]Dependency, 0, len(order))
	for _, parent := range order {
		out = append(out, Dependency{Ref: parent, DependsOn: edges[parent]})
	}
	return out
}

func findPkgVersion(pkgs []lockfile.LockedPackage, name string) string {
	for _, p := range pkgs {
		if p.Name == name {
			return p.Version
		}
	}
	return ""
}

func bdlPURL(name, version string) string {
	return "pkg:fglpkg/" + name + "@" + version
}

func mavenPURL(group, artifact, version string) string {
	return "pkg:maven/" + group + "/" + artifact + "@" + version
}

// deterministicUUID derives a stable, RFC-4122-shaped (version-4 layout) UUID
// string from seed via SHA-256. It is not random — the same seed always yields
// the same id — but is well-formed and distinct per distinct content, which is
// what the SBOM serial number needs (the value is not security-sensitive).
func deterministicUUID(seed string) string {
	h := sha256.Sum256([]byte(seed))
	var b [16]byte
	copy(b[:], h[:16])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4 layout
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
