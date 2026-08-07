package audit

import (
	"math"
	"strings"
)

// cvssBaseScore parses a CVSS v3.0/v3.1 vector string and returns its base
// score (0.0–10.0) and true. It returns (0, false) for any vector it cannot
// score: an empty string, a vector missing a base metric, or a non-v3 version
// (CVSS v2 has no "CVSS:" prefix; CVSS v4 scores via a lookup model this does
// not implement). Only the base metric group is used — temporal and
// environmental metrics are ignored, matching how OSV/GHSA report base scores.
//
// The arithmetic is the CVSS v3.1 specification, §7.1 (Base score). v3.0 uses
// the same metric weights and differs only in the final rounding; the v3.1
// Roundup is applied to both, which never shifts a score across a severity
// bucket boundary in practice.
func cvssBaseScore(vector string) (float64, bool) {
	v := strings.TrimSpace(vector)
	parts := strings.Split(v, "/")
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "CVSS:3") {
		return 0, false
	}

	m := make(map[string]string, len(parts))
	for _, p := range parts[1:] {
		if k, val, ok := strings.Cut(p, ":"); ok {
			m[k] = val
		}
	}

	scope := m["S"]
	scopeChanged := scope == "C"

	av, ok1 := map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}[m["AV"]]
	ac, ok2 := map[string]float64{"L": 0.77, "H": 0.44}[m["AC"]]
	ui, ok3 := map[string]float64{"N": 0.85, "R": 0.62}[m["UI"]]
	pr, ok4 := privilegesRequired(m["PR"], scopeChanged)
	conf, ok5 := impactMetric(m["C"])
	integ, ok6 := impactMetric(m["I"])
	avail, ok7 := impactMetric(m["A"])
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6 && ok7) || (scope != "U" && scope != "C") {
		return 0, false
	}

	iss := 1 - (1-conf)*(1-integ)*(1-avail)
	var impact float64
	if scopeChanged {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}
	if impact <= 0 {
		return 0, true // a well-formed vector with no impact scores 0.0.
	}

	exploitability := 8.22 * av * ac * pr * ui
	sum := impact + exploitability
	if scopeChanged {
		sum *= 1.08
	}
	return roundUpCVSS(math.Min(sum, 10)), true
}

// privilegesRequired returns the PR weight, which depends on Scope: an
// attacker needing privileges in a *changed* scope is weighted higher.
func privilegesRequired(v string, scopeChanged bool) (float64, bool) {
	switch v {
	case "N":
		return 0.85, true
	case "L":
		if scopeChanged {
			return 0.68, true
		}
		return 0.62, true
	case "H":
		if scopeChanged {
			return 0.5, true
		}
		return 0.27, true
	}
	return 0, false
}

// impactMetric returns the weight for a Confidentiality/Integrity/Availability
// metric (High/Low/None).
func impactMetric(v string) (float64, bool) {
	switch v {
	case "H":
		return 0.56, true
	case "L":
		return 0.22, true
	case "N":
		return 0, true
	}
	return 0, false
}

// roundUpCVSS is the CVSS v3.1 Roundup (spec §Appendix A): the smallest number
// to one decimal place that is >= input, computed on scaled integers so binary
// floating-point artifacts do not push a value the wrong way.
func roundUpCVSS(input float64) float64 {
	intInput := int(math.Round(input * 100000))
	if intInput%10000 == 0 {
		return float64(intInput) / 100000
	}
	return (math.Floor(float64(intInput)/10000) + 1) / 10
}

// severityFromCVSS maps a CVSS v3 base score to one of the four buckets using
// the qualitative severity-rating scale (spec §5). A 0.0 ("None") has no bucket
// of its own here and maps to low, the least-severe bucket we model.
func severityFromCVSS(score float64) string {
	switch {
	case score >= 9.0:
		return SeverityCritical
	case score >= 7.0:
		return SeverityHigh
	case score >= 4.0:
		return SeverityMedium
	default:
		return SeverityLow
	}
}
