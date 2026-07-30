package manifest_test

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestMavenURL covers the mirror-base rerouting for JAR downloads (GIS-365):
// an empty base preserves public Maven Central; a mirror base rewrites only the
// host/prefix while keeping the standard Maven2 layout; and a per-dependency
// URL override wins over both.
func TestMavenURL(t *testing.T) {
	dep := manifest.JavaDependency{
		GroupID:    "com.google.code.gson",
		ArtifactID: "gson",
		Version:    "2.10.1",
	}

	cases := []struct {
		name       string
		dep        manifest.JavaDependency
		mirrorBase string
		want       string
	}{
		{
			name: "empty base defaults to Maven Central",
			dep:  dep,
			want: "https://repo1.maven.org/maven2/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar",
		},
		{
			name:       "mirror base rewrites only the prefix, Maven2 layout preserved",
			dep:        dep,
			mirrorBase: "https://artifactory.acme.example/artifactory/libs-release",
			want:       "https://artifactory.acme.example/artifactory/libs-release/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar",
		},
		{
			name:       "trailing slash on the mirror base is normalized",
			dep:        dep,
			mirrorBase: "https://artifactory.acme.example/artifactory/libs-release/",
			want:       "https://artifactory.acme.example/artifactory/libs-release/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar",
		},
		{
			name: "per-dependency URL override wins over the mirror",
			dep: manifest.JavaDependency{
				GroupID:    "com.google.code.gson",
				ArtifactID: "gson",
				Version:    "2.10.1",
				URL:        "https://internal.example/custom/gson.jar",
			},
			mirrorBase: "https://artifactory.acme.example/artifactory/libs-release",
			want:       "https://internal.example/custom/gson.jar",
		},
		{
			name: "explicit jar filename is honored against the mirror",
			dep: manifest.JavaDependency{
				GroupID:    "org.example",
				ArtifactID: "lib",
				Version:    "3.0.0",
				JarFile:    "lib-3.0.0-shaded.jar",
			},
			mirrorBase: "https://mirror.example/m2",
			want:       "https://mirror.example/m2/org/example/lib/3.0.0/lib-3.0.0-shaded.jar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.dep.MavenURL(tc.mirrorBase); got != tc.want {
				t.Errorf("MavenURL(%q) = %q, want %q", tc.mirrorBase, got, tc.want)
			}
		})
	}
}
