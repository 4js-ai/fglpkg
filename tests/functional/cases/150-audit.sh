suite "audit (mock OSV)"

# The OSV mock returns a HIGH advisory when the queried PURL contains "vuln",
# and nothing otherwise — so "safe" = clean, "vulnerable" = a finding.

_audit_clean() {
  mock_osv_start
  mock_lock_with_jar com.example safe 1.0.0
  run audit
  assert_success
  assert_contains "No known vulnerabilities"
}
it "audit passes when no JAR has advisories" _audit_clean

_audit_findings() {
  mock_osv_start
  mock_lock_with_jar com.example vulnerable 1.0.0
  run audit
  assert_failure                       # finding >= default floor (medium) → exit 1
  assert_contains "vulnerability"
  assert_contains "CVE-2026-0001"
}
it "audit reports a vulnerability and exits non-zero" _audit_findings

_audit_severity_floor() {
  mock_osv_start
  mock_lock_with_jar com.example vulnerable 1.0.0
  run audit --severity=critical         # advisory is HIGH < critical → below floor
  assert_success
  run audit --severity=high             # HIGH >= high → at floor
  assert_failure
}
it "audit honors the --severity floor" _audit_severity_floor

_audit_production() {
  mock_osv_start
  mock_lock_with_jar com.example vulnerable 1.0.0 dev
  run audit
  assert_failure                        # dev-scoped vuln counts by default
  run audit --production                # --production skips dev-scoped JARs
  assert_success
  assert_contains "No Java JARs to audit"
}
it "audit --production skips dev-scoped JARs" _audit_production

_audit_json() {
  mock_osv_start
  mock_lock_with_jar com.example safe 1.0.0
  run audit --json
  assert_success
  printf '%s' "$output" > audit.json
  assert_json audit.json
  assert_contains '"source": "osv.dev"'
}
it "audit --json emits valid JSON" _audit_json

_audit_no_lock() {
  run audit                             # no fglpkg.lock present
  assert_exit 2                         # 2 = tool/setup failure (distinct from 1 = findings)
}
it "audit without a lockfile exits 2" _audit_no_lock

skip "audit signatures re-verifies Ed25519 registry signatures" "needs signed fixtures + a keys manifest — deferred (like login)"
