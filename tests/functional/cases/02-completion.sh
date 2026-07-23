suite "completion"

_completion_all() {
  local sh
  for sh in bash zsh fish powershell; do
    run completion "$sh"
    assert_success
    [[ -n "$output" ]] || { _diag "empty completion for $sh"; return 1; }
  done
}
it "completion emits a script for bash/zsh/fish/powershell" _completion_all

_completion_bad() { run completion nonsense; assert_failure; }
it "completion rejects an unknown shell" _completion_bad
