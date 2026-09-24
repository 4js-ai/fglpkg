suite "run: the project's own bin (GIS-566)"

# `fglpkg run` is not limited to installed packages: a bin declared in the
# current project's own fglpkg.json is listed and runnable, with project-first
# precedence.
_rpb_setup() {
  cat > fglpkg.json <<'EOF'
{ "name": "myproj", "version": "1.0.0", "genero": ">=3.20",
  "bin": { "greet": "scripts/greet.sh" } }
EOF
  mkdir -p scripts
  cat > scripts/greet.sh <<'EOF'
#!/bin/sh
echo "greet ran"
EOF
  chmod +x scripts/greet.sh
}

_rpb_list_shows_project_bin() {
  _rpb_setup
  run run --list
  assert_success
  assert_contains "greet"
  assert_contains "project"                 # the SOURCE column
  assert_not_contains "No commands available"
}
it "run --list includes the project's own bin" _rpb_list_shows_project_bin

_rpb_run_executes_project_bin() {
  _rpb_setup
  run run greet
  assert_success
  assert_contains "greet ran"               # the project's script actually ran
}
it "run <cmd> executes the project's own bin" _rpb_run_executes_project_bin
