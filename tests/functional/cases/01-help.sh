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

# GIS-593: `update` borrows install's flag parser, which collects positionals,
# and cmdUpdate never read them — so a package name, a nonexistent name and an
# unknown flag were all accepted and exited 0 while the whole graph re-resolved.
_update_rejects_pkg() { run update fglunit; assert_failure; assert_contains 'unknown argument "fglunit"'; }
it "update refuses a package argument" _update_rejects_pkg

_update_redirects_to_install() { run update fglunit; assert_contains "fglpkg install fglunit@<version>"; }
it "update points at install for moving one package" _update_redirects_to_install

_update_rejects_unknown_flag() { run update --bogus; assert_failure; assert_contains 'unknown argument "--bogus"'; }
it "update refuses an unknown flag" _update_rejects_unknown_flag
