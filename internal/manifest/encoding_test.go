package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateLengthSemantics(t *testing.T) {
	cases := []struct {
		name      string
		length    string
		encoding  string
		wantErr   bool
		wantInErr string
	}{
		{name: "absent is no constraint"},
		{name: "char", length: "char"},
		{name: "byte", length: "byte"},
		// The environment variable is spelled in upper case, so a manifest
		// copying that spelling must not be rejected.
		{name: "CHAR uppercase", length: "CHAR"},
		{name: "BYTE uppercase", length: "BYTE"},
		{name: "surrounding space", length: "  char  "},
		// A typo must not degrade to "no constraint" — silently ignoring it is
		// exactly the failure the field exists to prevent.
		{name: "typo", length: "chars", wantErr: true, wantInErr: "invalid lengthSemantics"},
		{name: "nonsense", length: "utf8", wantErr: true, wantInErr: "invalid lengthSemantics"},

		// char + a single-byte charset is unsatisfiable: the runtime reports
		// CHAR as BYTE under a single-byte charmap, so no consumer could ever
		// match it.
		{
			name: "char with single-byte charset", length: "char", encoding: "ISO-8859-15",
			wantErr: true, wantInErr: "single-byte",
		},
		{
			name: "char with US-ASCII", length: "char", encoding: "US-ASCII",
			wantErr: true, wantInErr: "single-byte",
		},
		// The combinations that do hold.
		{name: "char with UTF-8", length: "char", encoding: "UTF-8"},
		{name: "byte with UTF-8", length: "byte", encoding: "UTF-8"},
		{name: "byte with ISO-8859-15", length: "byte", encoding: "ISO-8859-15"},
		{name: "char with no encoding declared", length: "char"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{
				Name: "acme-widget", Version: "1.0.0",
				LengthSemantics: tc.length, CharacterEncoding: tc.encoding,
			}
			err := m.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Validate() = nil, want an error")
				}
				if !strings.Contains(err.Error(), tc.wantInErr) {
					t.Errorf("error %q does not mention %q", err, tc.wantInErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestNormalizedLengthSemantics(t *testing.T) {
	cases := []struct{ in, want string }{
		{"CHAR", "char"}, {"char", "char"}, {"  Byte ", "byte"}, {"", ""},
	}
	for _, tc := range cases {
		m := &Manifest{LengthSemantics: tc.in}
		if got := m.NormalizedLengthSemantics(); got != tc.want {
			t.Errorf("NormalizedLengthSemantics(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Both fields are omitempty: a package that declares neither must round-trip
// without growing two empty keys, because every manifest written before
// GIS-285 declares neither and republishing one must not change its shape.
func TestEncodingFieldsOmitEmpty(t *testing.T) {
	data, err := json.Marshal(&Manifest{Name: "acme-widget", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, key := range []string{"characterEncoding", "lengthSemantics"} {
		if strings.Contains(string(data), key) {
			t.Errorf("an undeclared %s was serialized: %s", key, data)
		}
	}
}

func TestEncodingFieldsRoundTrip(t *testing.T) {
	in := &Manifest{
		Name: "acme-widget", Version: "1.0.0",
		CharacterEncoding: "UTF-8", LengthSemantics: "char",
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out Manifest
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.CharacterEncoding != "UTF-8" || out.LengthSemantics != "char" {
		t.Errorf("round trip lost data: %+v", out)
	}
}
