// Package classpath maintains the CLASSPATH anchor jar: <jarsDir>/.classpath.jar,
// a minimal jar whose META-INF/MANIFEST.MF Class-Path attribute lists every
// dependency jar sitting beside it. fglpkg's CLASSPATH then carries the one
// constant anchor path per scope instead of enumerating every jar.
//
// The anchor exists because fglrun's embedded JVM (loaded via JNI, not the
// java/javac launcher) does not expand shell-style classpath wildcards
// (dir/*) — confirmed empirically: -Djava.class.path=dir/* fails to resolve
// any class in dir, while -Djava.class.path=<a single jar> works. A JAR's own
// MANIFEST.MF Class-Path attribute, in contrast, is a classloader-level
// feature (honored by java.net.URLClassLoader itself, not the launcher), so
// it works identically whether the JVM was started by java or embedded via
// JNI — confirmed empirically the same way.
//
// Write/read split: only the commands that mutate the jar set — install,
// update, remove — call Sync. `fglpkg env` and `fglpkg bdl` are read→stdout
// commands (env is typically wired into shell profiles via eval), so they
// must never write to disk: they call AnchorPath to REFERENCE the anchor when
// it exists, and warn when it is missing.
package classpath

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/atomicfile"
)

// AnchorName is the generated stub jar's filename. Dot-prefixed so it reads
// unambiguously as fglpkg-managed tooling, never as one of the real
// dependency jars sitting beside it.
const AnchorName = ".classpath.jar"

// AnchorPath returns the anchor's absolute path inside jarsDir when the
// anchor exists on disk, "" otherwise. Read-only — this is the env/bdl side
// of the write/read split (see the package comment).
func AnchorPath(jarsDir string) string {
	abs, err := filepath.Abs(jarsDir)
	if err != nil {
		return ""
	}
	p := filepath.Join(abs, AnchorName)
	if fi, err := os.Stat(p); err != nil || fi.IsDir() {
		return ""
	}
	return p
}

// HasDependencyJars reports whether jarsDir directly holds at least one real
// dependency jar (any *.jar other than the anchor itself). env uses this to
// warn when jars exist but the anchor is missing — instead of silently
// emitting no CLASSPATH.
func HasDependencyJars(jarsDir string) bool {
	return len(dependencyJars(jarsDir)) > 0
}

// Sync brings <jarsDir>/.classpath.jar in line with the dependency jars
// beside it: (re)writes the anchor when jars exist, deletes it when none
// remain, and leaves the file untouched when it is already current. The
// write is atomic (temp file + rename via atomicfile) because the anchor is
// read off a live CLASSPATH — a torn write would corrupt a classpath entry
// mid-run.
//
// install/update/remove call this after the jars dir settles; those are the
// only commands that change the jar set, so the anchor can never go stale
// between them. A missing jarsDir is a no-op.
func Sync(jarsDir string) error {
	abs, err := filepath.Abs(jarsDir)
	if err != nil {
		return nil
	}
	anchor := filepath.Join(abs, AnchorName)

	jarNames := dependencyJars(abs)
	if len(jarNames) == 0 {
		// No dependency jars (or no dir at all): a leftover anchor would put
		// a stale, pointless entry on CLASSPATH — remove it.
		if err := os.Remove(anchor); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}

	data, err := anchorJarBytes(jarNames)
	if err != nil {
		return err
	}
	// The jar bytes are deterministic (fixed manifest, zero zip timestamps),
	// so a byte-compare detects "already current" and skips the write — Sync
	// is safe to call from any install path, including no-op installs.
	if existing, err := os.ReadFile(anchor); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return atomicfile.WriteFile(anchor, data, 0o644)
}

// dependencyJars returns the sorted basenames of every *.jar directly in dir,
// excluding the anchor itself. Missing/unreadable dirs yield nil.
func dependencyJars(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == AnchorName || !strings.HasSuffix(e.Name(), ".jar") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// anchorJarBytes returns a minimal valid jar containing only a
// META-INF/MANIFEST.MF whose Class-Path attribute lists jarNames (plain
// filenames — every jar is a sibling of the anchor, which also makes the
// anchor path-independent: the jars dir can move without a rewrite).
func anchorJarBytes(jarNames []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(buildClasspathManifest(jarNames)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildClasspathManifest returns a JAR manifest (CRLF line endings, per the
// JAR spec) declaring Class-Path: <jarNames, space-separated>, with the
// Class-Path header wrapped at 72 bytes per line (including the line
// terminator) as the spec requires: continuation lines begin with a single
// space. A manifest header that violates this limit is corrupted silently
// from the JVM's point of view (it just won't parse as one logical value),
// so this must be exact, not approximate.
func buildClasspathManifest(jarNames []string) []byte {
	var b strings.Builder
	b.WriteString("Manifest-Version: 1.0\r\n")
	b.WriteString(wrapManifestHeader("Class-Path", strings.Join(jarNames, " ")))
	b.WriteString("\r\n")
	return []byte(b.String())
}

// wrapManifestHeader formats "name: value" per the JAR manifest spec's
// 72-bytes-per-line limit (the limit includes the CRLF terminator, so 70
// content bytes on the first line and 69 on each continuation line, which
// starts with a single leading space).
func wrapManifestHeader(name, value string) string {
	const maxLineBytes = 72
	line := name + ": " + value

	var b strings.Builder
	first := true
	for len(line) > 0 {
		limit := maxLineBytes - 2 // "\r\n"
		prefix := ""
		if !first {
			limit-- // leading continuation space
			prefix = " "
		}
		if len(line) <= limit {
			b.WriteString(prefix)
			b.WriteString(line)
			b.WriteString("\r\n")
			break
		}
		b.WriteString(prefix)
		b.WriteString(line[:limit])
		b.WriteString("\r\n")
		line = line[limit:]
		first = false
	}
	return b.String()
}
