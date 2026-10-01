package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
)

// GIS-574 follow-ups on the consuming commands that are not `install`.

// variantStub serves fglunit shaped like the production package, plus a 1.2.0
// that publishes a genero4 build but declares it needs Genero 6:
//
//	1.0.0  genero4, genero6
//	1.0.1  genero6
//	1.2.0  genero4, genero6  "genero": ">=6.00"
func variantStub(t *testing.T) {
	t.Helper()
	art := func(variant string) map[string]any {
		return map[string]any{"variant": variant, "sha256": variant, "download_url": "https://r2/" + variant + ".zip"}
	}
	detail := map[string]any{
		"slug": "fglunit",
		"name": "fglunit",
		"versions": []map[string]any{
			{"version": "1.0.0", "artifacts": []map[string]any{art("genero4"), art("genero6")}},
			{"version": "1.0.1", "artifacts": []map[string]any{art("genero6")}},
			{"version": "1.2.0", "genero": ">=6.00", "artifacts": []map[string]any{art("genero4"), art("genero6")}},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/registry/packages/") != "fglunit" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(detail)
	}))
	t.Cleanup(ts.Close)
	t.Setenv("FGLPKG_REGISTRY", ts.URL)
}

// `outdated` must name only versions `update` could select, or on Genero 4 it
// reports "update available" for ever and fails as a CI gate.
func TestBuildOutdatedRowHonoursTheRunningGenero(t *testing.T) {
	variantStub(t)

	for _, tc := range []struct {
		gv                     string
		wantWanted, wantLatest string
		wantStatus             string
	}{
		// 1.0.1 has no genero4 build; 1.2.0 declares >=6.00.
		{"4.01.12", "1.0.0", "1.0.0", "ok"},
		{"6.00.01", "1.2.0", "1.2.0", "update available"},
		{"3.20.05", "", "", "no version for Genero 3.20.05"},
	} {
		t.Run(tc.gv, func(t *testing.T) {
			gv := genero.MustParse(tc.gv)
			row := buildOutdatedRow(nil, "fglunit", "^1.0.0", "1.0.0", "", &gv)
			if row.Wanted != tc.wantWanted || row.Latest != tc.wantLatest || row.Status != tc.wantStatus {
				t.Errorf("Genero %s: wanted=%q latest=%q status=%q, want %q / %q / %q",
					tc.gv, row.Wanted, row.Latest, row.Status, tc.wantWanted, tc.wantLatest, tc.wantStatus)
			}
		})
	}

	// Undetected Genero filters nothing, as before.
	if row := buildOutdatedRow(nil, "fglunit", "^1.0.0", "1.0.0", "", nil); row.Wanted != "1.2.0" {
		t.Errorf("no Genero: wanted = %q, want 1.2.0", row.Wanted)
	}
}

// The multi-provider path carries variants through outdatedVersionList too.
func TestBuildOutdatedRowHonoursTheRunningGeneroMultiProvider(t *testing.T) {
	variantStub(t)
	rs := provider.NewRepositorySet([]provider.Provider{provider.NewGeneroProvider(config.GIName)}, nil, nil)

	gv := genero.MustParse("4.01.12")
	row := buildOutdatedRow(rs, "fglunit", "^1.0.0", "1.0.0", "", &gv)
	if row.Wanted != "1.0.0" || row.Status != "ok" {
		t.Errorf("wanted=%q status=%q, want 1.0.0 / ok", row.Wanted, row.Status)
	}
}

// With a second registry configured, `info` goes through the RepositorySet and
// passes the runtime's major. A version with no build for it must still be
// described rather than reported as an error: `info` installs nothing.
func TestInfoFetchDescribesAVersionWithNoBuildForThisGenero(t *testing.T) {
	variantStub(t)
	t.Setenv("FGLPKG_GENERO_VERSION", "4.01.12")
	rs := provider.NewRepositorySet([]provider.Provider{provider.NewGeneroProvider(config.GIName)}, nil, nil)

	info, err := infoFetch(rs, "fglunit", "1.0.1")
	if err != nil {
		t.Fatalf("info must describe fglunit@1.0.1 on Genero 4: %v", err)
	}
	if info.Variant != "genero6" {
		t.Errorf("Variant = %q, want the only published build %q", info.Variant, "genero6")
	}

	// Where the runtime does have a build, that is the one described.
	info, err = infoFetch(rs, "fglunit", "1.0.0")
	if err != nil {
		t.Fatalf("infoFetch fglunit@1.0.0: %v", err)
	}
	if info.Variant != "genero4" {
		t.Errorf("Variant = %q, want the runtime's build %q", info.Variant, "genero4")
	}

	// Only the no-build case falls back; anything else is still an error.
	if _, err := infoFetch(rs, "fglunit", "9.9.9"); err == nil {
		t.Error("infoFetch of a nonexistent version must still fail")
	}
}
