suite "help & usage"

_help_lists() { run help; assert_success; assert_contains "COMMANDS:"; assert_contains "pack"; assert_contains "publish"; assert_contains "install"; }
it "help lists the command surface" _help_lists

_unknown_cmd() { run boguscmd; assert_failure; }
it "unknown command exits non-zero" _unknown_cmd

_cmd_help() { run pack --help; assert_success; assert_contains "USAGE"; }
it "<command> --help shows usage" _cmd_help

# The other half of the Genero convention in SUPASIA-3231: -h for help. This
# already worked; the case pins it so it stays working alongside -V.
_help_flag() { run -h; assert_success; assert_contains "COMMANDS:"; }
it "-h shows usage" _help_flag

# A user who types the wrong version flag should be able to find the right one
# in the usage page rather than guessing (SUPASIA-3231).
_global_flags() { run help; assert_success; assert_contains "GLOBAL FLAGS:"; assert_contains "-V, -v, --version"; }
it "help documents the global flags" _global_flags
