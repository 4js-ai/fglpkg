suite "sbom"

_sbom_lock() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg","version":"1.0.0","description":"d","genero":">=3.20" }
EOF
  cat > fglpkg-lock.json <<'EOF'
{ "lockfileVersion":1, "generatedAt":"2026-01-01T00:00:00Z", "generoVersion":"6.00.01",
  "root":{"name":"demo.pkg","version":"1.0.0"}, "packages":[], "jars":[] }
EOF
  run sbom --pretty
  assert_success
  assert_contains "CycloneDX"
  assert_contains "1.5"
}
it "sbom emits a CycloneDX 1.5 document from the lockfile" _sbom_lock

_sbom_file() {
  cat > fglpkg.json <<'EOF'
{ "name":"demo.pkg","version":"1.0.0","genero":">=3.20" }
EOF
  cat > fglpkg-lock.json <<'EOF'
{ "lockfileVersion":1, "generatedAt":"2026-01-01T00:00:00Z", "generoVersion":"6.00.01",
  "root":{"name":"demo.pkg","version":"1.0.0"}, "packages":[], "jars":[] }
EOF
  run sbom -o sbom.json
  assert_success
  assert_file sbom.json
  assert_json sbom.json
  assert_json_field sbom.json bomFormat CycloneDX
}
it "sbom -o writes valid CycloneDX JSON" _sbom_file
