package audit

import (
	"math"
	"testing"
)

func TestCVSSBaseScore(t *testing.T) {
	cases := []struct {
		name   string
		vector string
		want   float64
		ok     bool
	}{
		{
			name:   "v3.1 critical 9.8 (network RCE, full impact)",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
			want:   9.8, ok: true,
		},
		{
			name:   "v3.1 medium 4.3 (network, UI required, low conf only)",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:U/C:L/I:N/A:N",
			want:   4.3, ok: true,
		},
		{
			name:   "v3.1 scope-changed critical 10.0",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H",
			want:   10.0, ok: true,
		},
		{
			name:   "v3.0 is scored like v3.1",
			vector: "CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",
			want:   9.8, ok: true,
		},
		{
			name:   "no-impact vector scores 0.0 but is well-formed",
			vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N",
			want:   0.0, ok: true,
		},
		{name: "empty string", vector: "", ok: false},
		{name: "CVSS v2 (no prefix) is not scored here", vector: "AV:N/AC:L/Au:N/C:P/I:P/A:P", ok: false},
		{name: "CVSS v4 is not scored here", vector: "CVSS:4.0/AV:N/AC:L/AT:N/PR:N/UI:N/VC:H/VI:H/VA:H/SC:N/SI:N/SA:N", ok: false},
		{name: "missing a base metric (no A)", vector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H", ok: false},
		{name: "unknown metric value", vector: "CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cvssBaseScore(tc.vector)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (score %v)", ok, tc.ok, got)
			}
			if ok && math.Abs(got-tc.want) > 0.001 {
				t.Errorf("score = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSeverityFromCVSS(t *testing.T) {
	cases := []struct {
		score float64
		want  string
	}{
		{10.0, SeverityCritical},
		{9.0, SeverityCritical},
		{8.9, SeverityHigh},
		{7.0, SeverityHigh},
		{6.9, SeverityMedium},
		{4.0, SeverityMedium},
		{3.9, SeverityLow},
		{0.1, SeverityLow},
		{0.0, SeverityLow}, // "None" has no bucket of its own; maps to low.
	}
	for _, tc := range cases {
		if got := severityFromCVSS(tc.score); got != tc.want {
			t.Errorf("severityFromCVSS(%v) = %q, want %q", tc.score, got, tc.want)
		}
	}
}
