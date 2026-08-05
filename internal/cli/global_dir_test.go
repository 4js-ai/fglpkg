package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// fglpkgGlobalDir splits the global package root from the config/credentials home
// (GIS-367). Precedence: FGLPKG_GLOBAL_DIR > FGLPKG_HOME > $FGLDIR/fglpkg (if
// writable) > ~/.fglpkg.
func TestFglpkgGlobalDir(t *testing.T) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	defaultHome := filepath.Join(userHome, ".fglpkg")

	writableFGLDIR := t.TempDir() // a real, writable dir → $FGLDIR/fglpkg is usable
	fileAsFGLDIR := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(fileAsFGLDIR, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	// A stale/mistyped FGLDIR: the path does not exist, but its parent (a temp
	// dir) is writable — so a create-a-file probe alone would wrongly accept it.
	missingFGLDIR := filepath.Join(t.TempDir(), "uninstalled-genero")

	cases := []struct {
		name      string
		globalDir string
		home      string
		fgldir    string
		want      string
	}{
		{
			name:      "FGLPKG_GLOBAL_DIR wins over everything",
			globalDir: "/opt/pkgs",
			home:      "/some/home",
			fgldir:    writableFGLDIR,
			want:      "/opt/pkgs",
		},
		{
			name:   "FGLPKG_HOME governs the package root when no GLOBAL_DIR (back-compat, beats a writable FGLDIR)",
			home:   "/my/home",
			fgldir: writableFGLDIR,
			want:   "/my/home",
		},
		{
			name:   "writable FGLDIR is used when neither GLOBAL_DIR nor HOME is set",
			fgldir: writableFGLDIR,
			want:   filepath.Join(writableFGLDIR, "fglpkg"),
		},
		{
			name:   "file-as-FGLDIR falls back to ~/.fglpkg",
			fgldir: fileAsFGLDIR, // FGLDIR is a file, not a directory → rejected
			want:   defaultHome,
		},
		{
			name:   "non-existent FGLDIR falls back to ~/.fglpkg",
			fgldir: missingFGLDIR, // stale/mistyped FGLDIR must not capture the store
			want:   defaultHome,
		},
		{
			name: "no FGLDIR falls back to ~/.fglpkg",
			want: defaultHome,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FGLPKG_GLOBAL_DIR", tc.globalDir)
			t.Setenv("FGLPKG_HOME", tc.home)
			t.Setenv("FGLDIR", tc.fgldir)

			got, err := fglpkgGlobalDir()
			if err != nil {
				t.Fatalf("fglpkgGlobalDir: %v", err)
			}
			if got != tc.want {
				t.Errorf("fglpkgGlobalDir() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fglpkgGlobalDir must never move the config/credentials home: fglpkgHome() stays
// on FGLPKG_HOME/~/.fglpkg even when packages are bound to FGLDIR.
func TestGlobalDirDoesNotMoveConfigHome(t *testing.T) {
	fgldir := t.TempDir()
	t.Setenv("FGLPKG_GLOBAL_DIR", "")
	t.Setenv("FGLPKG_HOME", "")
	t.Setenv("FGLDIR", fgldir)

	pkgRoot, err := fglpkgGlobalDir()
	if err != nil {
		t.Fatalf("fglpkgGlobalDir: %v", err)
	}
	cfgHome, err := fglpkgHome()
	if err != nil {
		t.Fatalf("fglpkgHome: %v", err)
	}
	if pkgRoot == cfgHome {
		t.Fatalf("expected the package root (%q) to differ from the config/credentials home (%q) when FGLDIR is writable", pkgRoot, cfgHome)
	}
	if want := filepath.Join(fgldir, "fglpkg"); pkgRoot != want {
		t.Errorf("package root = %q, want %q", pkgRoot, want)
	}
}

func TestDirWritable(t *testing.T) {
	dir := t.TempDir()

	if !dirWritable(dir) {
		t.Errorf("dirWritable(%q) = false, want true (a fresh temp dir is writable)", dir)
	}
	// A not-yet-existing path whose nearest existing ancestor is writable.
	if sub := filepath.Join(dir, "a", "b"); !dirWritable(sub) {
		t.Errorf("dirWritable(%q) = false, want true (creatable under a writable ancestor)", sub)
	}
	// A file is not a writable directory.
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if dirWritable(file) {
		t.Errorf("dirWritable(%q) = true, want false (it is a file)", file)
	}
	// A path under a file cannot be created.
	if under := filepath.Join(file, "child"); dirWritable(under) {
		t.Errorf("dirWritable(%q) = true, want false (parent is a file)", under)
	}
}
