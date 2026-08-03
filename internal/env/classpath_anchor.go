package env

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// classpathAnchorName is the generated stub jar's filename. Dot-prefixed so
// it reads unambiguously as fglpkg-managed tooling, never as one of the
// real dependency jars sitting beside it.
const classpathAnchorName = ".classpath.jar"

// classpathAnchorPath scans dir for dependency jars (every *.jar other than
// the anchor itself) and, if any exist, (re)writes dir/.classpath.jar with a
// MANIFEST.MF Class-Path entry listing them all by filename, returning the
// anchor's path. Returns "" (no error) if dir doesn't exist or has no jars.
//
// This exists because fglrun's embedded JVM (loaded via JNI, not the java/
// javac launcher) does not expand shell-style classpath wildcards
// (dir/*) — confirmed empirically: -Djava.class.path=dir/* fails to
// resolve any class in dir, while -Djava.class.path=<a single jar> works.
// A JAR's own MANIFEST.MF Class-Path attribute, in contrast, is a
// classloader-level feature (honored by java.net.URLClassLoader itself,
// not the launcher), so it works identically whether the JVM was started
// by java or embedded via JNI — confirmed empirically the same way.
//
// Regenerated on every call (matching buildJavaClasspath's own no-caching,
// always-fresh-scan design) rather than only at install time, so it can
// never go stale relative to what's actually in dir.
func classpathAnchorPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil
	}
	entries, err := os.ReadDir(abs)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	var jarNames []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == classpathAnchorName || !strings.HasSuffix(e.Name(), ".jar") {
			continue
		}
		jarNames = append(jarNames, e.Name())
	}
	if len(jarNames) == 0 {
		return "", nil
	}
	sort.Strings(jarNames)

	anchorPath := filepath.Join(abs, classpathAnchorName)
	if err := writeClasspathAnchorJar(anchorPath, jarNames); err != nil {
		return "", err
	}
	return anchorPath, nil
}

// writeClasspathAnchorJar writes a minimal valid jar at path containing only
// a META-INF/MANIFEST.MF whose Class-Path attribute lists jarNames (each
// resolved relative to the anchor's own directory, i.e. plain filenames
// since every jar is a sibling of the anchor).
func writeClasspathAnchorJar(path string, jarNames []string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("META-INF/MANIFEST.MF")
	if err != nil {
		return err
	}
	if _, err := w.Write(buildClasspathManifest(jarNames)); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
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
