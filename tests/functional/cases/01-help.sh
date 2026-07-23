suite "help & usage"

_help_lists() { run help; assert_success; assert_contains "COMMANDS:"; assert_contains "pack"; assert_contains "publish"; assert_contains "install"; }
it "help lists the command surface" _help_lists

_unknown_cmd() { run boguscmd; assert_failure; }
it "unknown command exits non-zero" _unknown_cmd

_cmd_help() { run pack --help; assert_success; assert_contains "USAGE"; }
it "<command> --help shows usage" _cmd_help
