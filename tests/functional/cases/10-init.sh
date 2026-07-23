suite "init"

# init is non-interactive with --yes (and on any non-TTY stdin, e.g. the pipe
# the harness feeds): it accepts defaults for every field instead of prompting.
# In a fresh, non-git dir that means the directory name, version 0.1.0, and
# license UNLICENSED — a structurally valid manifest.
_init_defaults() {
  run init --yes
  assert_success
  assert_file fglpkg.json
  assert_json fglpkg.json
  assert_json_field fglpkg.json version 0.1.0
  assert_json_field fglpkg.json license UNLICENSED
}
it "init --yes writes a valid fglpkg.json with defaults" _init_defaults

# In a git repo with an origin remote, init auto-detects the repository and
# normalises an SCP-style remote to an https URL.
_init_detects_repo() {
  run_raw git init -q
  run_raw git remote add origin git@github.com:acme/widget.git
  run init --yes
  assert_success
  assert_json_field fglpkg.json repository https://github.com/acme/widget
}
it "init --yes auto-detects the git origin as repository" _init_detects_repo

# --template library scaffolds files alongside the manifest.
_init_template() {
  run init --template library --yes
  assert_success
  assert_file fglpkg.json
  assert_json fglpkg.json
  assert_file .gitignore
}
it "init --template library scaffolds a manifest and files" _init_template
