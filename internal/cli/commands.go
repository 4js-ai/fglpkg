package cli

import (
	"fmt"
	"strings"
)

// command is one entry in the CLI help/usage registry. It is the single
// source of truth for a command's user-facing documentation: the top-level
// `printUsage` listing, per-command `--help` output, and the shell-completion
// command list all read from here.
//
// It deliberately does NOT own dispatch — the switch in Execute still routes
// each command to its handler. The registry only describes commands; keeping
// the two in sync is covered by TestRegistryMatchesDispatch.
type command struct {
	Name       string   // canonical command name (matches the dispatch switch)
	Aliases    []string // alternate names accepted by the dispatch switch
	Summary    string   // core one-line description, shown both in the top-level list and as the --help header
	ListDetail string   // extra appended to Summary in the top-level list ONLY (never in the command's own --help header). Include the leading separator: a space to stay on the same line, or a newline to wrap onto a hang-indented continuation line.
	Args       string   // compact positional-argument hint for the top-level list (e.g. "[pkg...]", "<pkg>"); "" when none
	Usage      string   // synopsis line(s); rendered under "USAGE:" (no "fglpkg " prefix needed — it's included)
	Long       string   // optional detailed body (flags, notes, examples), shown by --help

	// Passthrough marks commands that forward trailing arguments to a child
	// process (run, bdl). For these, -h/--help is only treated as a request
	// for fglpkg's help when it is the FIRST argument; otherwise it belongs
	// to the invoked program and must be passed through untouched.
	Passthrough bool
}

// commands is the ordered command registry. Order controls the top-level
// `printUsage` listing, so keep it grouped logically rather than alphabetical.
var commands = []command{
	{
		Name:       "init",
		Summary:    "Create a new fglpkg.json",
		ListDetail: " (--template <library|app> to scaffold)",
		Usage:      "fglpkg init [--template <library|app>] [--yes]",
		Long: `FLAGS:
  --template, -t <name>    Scaffold from a built-in template (library|app)
  --yes, -y                Skip prompts and accept all defaults

Prompts for every field 'fglpkg publish' requires — name, version,
description, author, license, and repository — then writes fglpkg.json.
The repository is auto-detected from the git 'origin' remote when present
(SCP-style remotes are normalised to https) and offered as the default;
otherwise you are prompted for it. A final prompt offers the optional
Genero version constraint, showing the detected runtime version.

With --yes, or when stdin is not a terminal (CI, scaffolding), init runs
non-interactively: it accepts the defaults (directory name, version 0.1.0,
UNLICENSED, detected repository) without prompting.
`,
	},
	{
		Name:    "install",
		Summary: "Install all dependencies (or add specific packages)",
		Args:    "[pkg...]",
		Usage:   "fglpkg install [package[@version]...] [flags]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
  --force, -f              Delete fglpkg.lock and .fglpkg/ first, then
                           re-download every package (local installs only)
  --save-dev, -D           Record added packages under "devDependencies"
  --save-optional, -O      Record added packages under "optionalDependencies"
  --save-prod, -P          Record added packages under "dependencies" (default)
  --production, --prod     Skip devDependencies when installing
  --registry <name>        When adding a package, resolve it only from the named
                           repository and pin that choice in fglpkg.json. Use to
                           disambiguate a name available from more than one repo.
  --no-manifest-fallback   Do not install Java dependencies a package's bundled
                           manifest declares but its registry record omits; the
                           divergence is still reported
  --no-verify-signature    Skip Layer 1 registry signature verification for this
                           install (discouraged; overrides signing.enforce)
  --no-prune               Keep packages, webcomponents, and JARs the dependency
                           graph no longer requires instead of deleting them
  --frozen                 Fail if fglpkg.lock disagrees with fglpkg.json instead
                           of re-resolving. For CI and deployment builds

With no package arguments, installs everything declared in fglpkg.json.

Install converges .fglpkg/ on fglpkg.json: a dependency you delete from the
manifest by hand is re-resolved out of fglpkg.lock AND deleted from disk, so it
stops appearing in 'fglpkg list' and stops resolving on FGLLDPATH. Pruning
applies to local (.fglpkg/) installs only — a global ~/.fglpkg/ is shared
across projects — and is skipped under --production, which resolves a
deliberately narrowed graph.

Installed packages are verified against the registry's Ed25519 signature by
default (mode "warn": a bad or missing signature warns but does not block).
Set signing.enforce to "require" in ~/.fglpkg/config.json, or FGLPKG_SIGNING=
require|warn|off, to change this.
With one or more <package>[@<version>] arguments, resolves and adds them.
Without --local/--global, the target is auto-detected: local when a
.fglpkg/ directory or fglpkg.json exists in the current directory.
`,
	},
	{
		Name:    "remove",
		Summary: "Remove an installed package (from fglpkg install)",
		Args:    "<pkg>",
		Usage:   "fglpkg remove <package>... [--local|--global]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)

Drops the package from fglpkg.json, re-resolves the remaining graph, and
rewrites fglpkg.lock. For a local (.fglpkg/) install it also prunes packages
and Java JARs the graph no longer needs. Global (~/.fglpkg/) artifacts are
shared across projects and are left on disk.
`,
	},
	{
		Name:    "update",
		Summary: "Re-resolve and update all dependencies",
		Usage:   "fglpkg update [--local|--global]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
  --registry <name>        Restrict this re-resolution to the named repository
  --production, --prod     Skip devDependencies (does not rewrite fglpkg.lock)
  --no-manifest-fallback   Do not install Java dependencies a package's bundled
                           manifest declares but its registry record omits; the
                           divergence is still reported
  --no-prune               Keep packages, webcomponents, and JARs the dependency
                           graph no longer requires instead of deleting them

Ignores fglpkg.lock and re-resolves every dependency to the newest version
allowed by the manifest constraints.

Like install, update then converges .fglpkg/ on the resolved graph: anything it
no longer requires is deleted from disk. Local (.fglpkg/) installs only — a
global ~/.fglpkg/ is shared across projects.
`,
	},
	{
		Name:    "list",
		Summary: "List installed packages",
		Usage:   "fglpkg list [--local|--global]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
`,
	},
	{
		Name:    "env",
		Summary: "Print environment variable exports",
		Usage:   "fglpkg env [--local|--global|--gst|--gwa] [--shell sh|powershell|cmd]",
		Long: `FLAGS:
  --local, -l              Force local project directory (.fglpkg/)
  --global, -g             Force global home directory (~/.fglpkg/)
  --gst                    Output in Genero Studio format (implies --local)
  --gwa                    Emit --webcomponent flags for gwabuildtool, one
                           per installed COMPONENTTYPE
  --shell <name>           Shell syntax to emit: sh (aliases bash, zsh),
                           powershell (alias pwsh), or cmd. Defaults to cmd on
                           Windows and sh elsewhere. Not valid with --gst/--gwa.

Prints shell statements that prepend the package paths. Load them with:
  sh / bash / zsh    eval "$(fglpkg env --global)"
  PowerShell         fglpkg env --global --shell powershell | Invoke-Expression
  Command Prompt     fglpkg env --global --shell cmd > setup-env.bat
                     call setup-env.bat

Use --shell sh under Git Bash or WSL on Windows. Values are quoted only when
they contain a character the shell would otherwise split on, so paths of
ordinary characters are emitted exactly as earlier releases emitted them.

Command Prompt cannot represent a path containing a literal '%' or '"'; fglpkg
warns on stderr and emits the line anyway. Use --shell powershell for those.
Prefer a .bat file over 'FOR /F ... DO %i': a %VAR% reference read from stdout
is not re-expanded, so that recipe silently loses the inherited value.

VARIABLES (each emitted only when a package ships matching files):
  FGLLDPATH                program modules — .42m/.42r/.42x
  CLASSPATH                Java jars
  FGLRESOURCEPATH          .42f .42s .4ad .4st .4sm .4tb .4tm .iem
  FGLDBPATH                .sch .val .att
  FGLIMAGEPATH             webcomponents + .png .jpg .gif .svg .bmp .ico
                           .tiff .ttf
  FGLPROFILE               config files declared by a package's "profile"

Existing values are preserved — fglpkg prepends. Diagnostics (such as two
packages shipping the same resource basename, where first-on-path wins) are
written to STDERR so stdout stays safe to eval.
`,
	},
	{
		Name:    "relink",
		Summary: "Rebuild the merged FGLLDPATH root from installed packages",
		Usage:   "fglpkg relink [--local|--global]",
		Long: `FLAGS:
  --local, -l              Rebuild the local project root (.fglpkg/merged) only
  --global, -g             Rebuild the global root (~/.fglpkg/merged) only

Rebuilds the derived, PACKAGE-correct merged FGLLDPATH root(s) from the
installed per-package stores. install/remove keep this current automatically;
run relink to recover a merged root that was deleted (it is gitignored) or left
stale by a manual edit of .fglpkg/packages. Idempotent. With no flag it rebuilds
the local root (when run inside a project) and the global root.
`,
	},
	{
		Name:       "search",
		Summary:    "Search the registry",
		ListDetail: " (use --all to list every package)",
		Args:       "<term>",
		Usage:      "fglpkg search <term> [--registry <name>]\nfglpkg search --all [--registry <name>]",
		Long: `FLAGS:
  --all                    List every package in the registry (no term)
  --genero <version>       Grade results against this Genero version instead
                           of the detected one (overrides FGLPKG_GENERO_VERSION)
  --registry <name>        Search only the named repository (results are still
                           source-tagged). Errors if the name isn't a configured
                           registry.

Each result is annotated with a compatibility marker against the running Genero
version (detected, or overridden with --genero / FGLPKG_GENERO_VERSION):
  ✓  compatible    ✗  incompatible    ?  unknown (no constraint / version)
Nothing is hidden or reordered — the marker is advisory. Registries that don't
report a Genero constraint, or a version that can't be detected, show ?.

When secondary repositories are configured, search fans out to every repository
and tags each result with its source repo. A repository that fails to respond
is reported as a warning without failing the whole search. (Compatibility is
not graded for secondary-repository results — they always show ?.)
`,
	},
	{
		Name:       "info",
		Aliases:    []string{"view"},
		Summary:    "Show registry metadata for a package",
		ListDetail: " (--json for raw output)",
		Args:       "<pkg>[@ver]",
		Usage:      "fglpkg info <package>[@<version>] [--json]",
		Long: `FLAGS:
  --json                   Emit raw PackageInfo JSON instead of a summary
`,
	},
	{
		Name:       "outdated",
		Summary:    "Show FGL deps with newer versions available",
		ListDetail: " (--json for JSON)",
		Usage:      "fglpkg outdated [--json]",
		Long: `FLAGS:
  --json                   Emit a JSON array instead of a table

Exits non-zero when any dependency is outdated, for use as a CI gate.
Java dependencies are not checked (they use exact version pins).
`,
	},
	{
		Name:       "audit",
		Summary:    "Check installed Java JARs for known vulnerabilities",
		ListDetail: "\n(--json, --severity=<level>, --production; or `audit signatures`)",
		Usage:      "fglpkg audit [flags]   |   fglpkg audit signatures",
		Long: `FLAGS:
  --json                          Emit a JSON report on stdout
  --severity=<low|medium|high|critical>
                                  Minimum severity that fails the build (default: medium)
  --production, --prod            Skip dev-scoped JARs
  --offline                       Reserved for a future cached-advisory mode (errors today)

SUBCOMMANDS:
  signatures                      Re-verify the Layer 1 registry signature of
                                  every package in the lock file against the
                                  current keys manifest. Exits non-zero if any
                                  package is unsigned or fails verification.

EXIT CODES:
  0  no findings at or above --severity (or all signatures valid)
  1  one or more findings at or above --severity (or a signature failed)
  2  audit itself failed (missing lockfile, network error, etc.)

NOTES:
  Java JARs are audited against the OSV.dev v1 API (anonymous, free).
  BDL packages are not scanned in this version (no public advisory feed).
`,
	},
	{
		Name:       "sbom",
		Summary:    "Emit a CycloneDX SBOM for the project from fglpkg.lock",
		ListDetail: "\n(-o file, --pretty, --production)",
		Usage:      "fglpkg sbom [flags]",
		Long: `FLAGS:
  -o, --output <path>             Write to file instead of stdout
  --pretty                        Indented JSON (default: compact)
  --production, --prod            Skip dev-scoped JARs
  --format=<cyclonedx|spdx>       Output format. Default: cyclonedx
                                  (spdx is reserved for a future release)

NOTES:
  v1 emits CycloneDX 1.5 JSON, generated from fglpkg.lock. No network calls.
  The serial number is derived from the content, so it is stable across runs
  for the same lockfile; set SOURCE_DATE_EPOCH for a byte-reproducible
  timestamp (otherwise the timestamp reflects the current time).
`,
	},
	{
		Name:       "completion",
		Summary:    "Print shell completion script",
		ListDetail: " (bash|zsh|fish|powershell)",
		Args:       "<sh>",
		Usage:      "fglpkg completion <bash|zsh|fish|powershell>",
		Long: `Install (bash):  fglpkg completion bash > /etc/bash_completion.d/fglpkg
Or source:       source <(fglpkg completion bash)
`,
	},
	{
		Name:        "bdl",
		Summary:     "Run a BDL program from an installed package",
		Args:        "<pkg> <mod>",
		Usage:       "fglpkg bdl <package> <module> [args...]\nfglpkg bdl --list",
		Passthrough: true,
		Long: `FLAGS:
  --list, -l               List BDL programs across installed packages

Runs a program declared in an installed package's "programs" list via fglrun.
Arguments after the module name are passed to the program unchanged.
`,
	},
	{
		Name:    "publish",
		Summary: "Publish current package to the registry; submits for admin review",
		ListDetail: "\n(--dry-run prints what would happen without calling out;\n" +
			" --ci for non-interactive pipelines: requires FGLPKG_TOKEN,\n" +
			" prints a machine-readable status line)",
		Usage: "fglpkg publish [--dry-run] [--ci] [--private|--public] [--changelog <text>] [--registry <name>] [--force]",
		Long: `FLAGS:
  --dry-run, -n            Print what would happen without any network calls
  --ci                     Non-interactive mode for pipelines: requires
                           FGLPKG_TOKEN and prints a machine-readable status line
  --private                Mark the package private on first publish
  --public                 Mark the package public on first publish (default)
  --changelog <text>       Changelog text for this version (overrides CHANGELOG.md)
  --registry <name>        Publish to a configured repository (e.g. a JFrog
                           Artifactory repo) instead of the GI registry
  --force, -f              When publishing to Artifactory, overwrite an existing
                           variant instead of refusing

Builds the package zip, uploads it, and submits the version for admin review.
When --registry names an Artifactory repository, the zip and its sidecar
fglpkg.json are deployed directly (no submit/approval step).

DEFAULT TARGET:
  With no --registry, publish targets the default repository, resolved in
  decreasing precedence: the FGLPKG_PUBLISH_REGISTRY environment variable, the
  project's "defaultRegistry" field in fglpkg.json, then the global
  ~/.fglpkg/config.json "defaultRegistry". If none is set, publish goes to the
  GI registry (the historical default). A team publishing to their own
  Artifactory can set "defaultRegistry" once and omit --registry thereafter.

CHANGELOG:
  When --changelog is not given, publish looks for a CHANGELOG.md in the project
  root and sends the section whose heading names the version being published
  (Keep a Changelog format, e.g. "## [1.2.0]"). If the file exists but has no
  entry for the version, the changelog is sent empty and a warning is printed.
`,
	},
	{
		Name:       "deprecate",
		Summary:    "Mark a published version or package deprecated",
		ListDetail: "\n(npm-style: stays installable, warns consumers;\n --moved-to <pkg> records a successor/rename; --undo lifts it)",
		Args:       "<pkg>",
		Usage: "fglpkg deprecate <pkg>[@<version>] [<message>] [--moved-to <newpkg>[@<version>]]\n" +
			"fglpkg deprecate <pkg>[@<version>] --message <text> [--moved-to <newpkg>]\n" +
			"fglpkg deprecate <pkg>[@<version>] --undo",
		Long: `FLAGS:
  --message <text>         The deprecation message (npm-style). Alternative to
                           passing the message as a positional argument.
  --moved-to <newpkg>[@<version>]
                           Record a successor package (the rename/relocation
                           case). Auto-fills the message "<pkg> has moved to
                           <newpkg>" when no message is given.
  --undo                   Lift the deprecation. Forbids a message / --moved-to.
  --json                   Emit a machine-readable result instead of text.

Deprecation is advisory (the npm model): a deprecated version stays fully
installable and listed — consumers just get a non-fatal warning on install,
'info', and 'outdated', pointing at the successor when --moved-to is set. With
a bare <pkg> (no @version) the whole package is deprecated/relocated. This is
an owner-only write and requires login. Deprecation does NOT withdraw or hide a
package (that is a separate operation).
`,
	},
	{
		Name:       "pack",
		Summary:    "Build the publishable zip locally without uploading",
		ListDetail: "\n(--list prints contents without writing a file)",
		Args:       "[-o file]",
		Usage:      "fglpkg pack [-o <file>] [--list]",
		Long: `FLAGS:
  -o, --output <file>      Write the zip to <file>
  --list, -l               Print the zip contents and metadata without writing

Builds the same zip 'fglpkg publish' would upload, for local inspection.
`,
	},
	{
		Name:    "lint",
		Aliases: []string{"check"},
		Summary: "Validate fglpkg.json before packing or publishing",
		Usage:   "fglpkg lint",
		Long: `Checks fglpkg.json and the package it would produce, printing an
errors + warnings report. Exits non-zero when any error is found, so it can
gate CI.

Reports (among others):
  - malformed or mistyped manifest fields, named in plain language
  - a 'files' or 'docs' pattern that matches no files
  - a declared 'program' with no matching staged .42m module
  - a package that would contain no BDL modules at all (error)
  - missing publish metadata: description, license, repository, author (warning)

The same validation runs automatically inside 'fglpkg pack' and
'fglpkg publish', so these problems cannot be skipped. Errors block; warnings
are advisory and do not stop pack/publish on their own.
`,
	},
	{
		Name:       "login",
		Summary:    "Sign in to the registry",
		ListDetail: " (OAuth browser flow, or --token <PAT>)",
		Usage:      "fglpkg login [--token <PAT>]\nfglpkg login --registry <name> [--token <t> | --user <u> --password <p> | --api-key <k>]",
		Long: `FLAGS:
  --token <PAT>            Store a Personal Access Token instead of the
                           browser OAuth flow (for CI / non-interactive use)
  --registry <name>        Sign in to a configured secondary repository (e.g. a
                           JFrog Artifactory repo) instead of the default GI
                           registry. The credential type follows the repo's
                           declared auth scheme (see below).
  --user <u> --password <p>  Basic auth for a --registry with auth "basic"
                           (the password may be an account password or a token)
  --api-key <k>            API key for a --registry with auth "apikey"

With no flags, opens a browser to complete an OAuth (code + PKCE) login to the
GI registry.

SECONDARY REPOSITORIES:
  'fglpkg login --registry <name>' stores credentials for a repository declared
  in fglpkg.json / ~/.fglpkg/config.json, keyed by its URL. The flag to use
  depends on that repo's "auth" scheme:
    bearer     --token <access-token>        (recommended for Artifactory)
    basic      --user <u> --password <p|token>
    apikey     --api-key <key>
    anonymous  no login needed
  Credentials for GI and every secondary repo coexist — logging in to one never
  affects another.

NOTE:
  FGLPKG_TOKEN, when set, authenticates the GI registry ahead of any stored
  login, so a GI login has no visible effect until that variable is unset.
`,
	},
	{
		Name:    "logout",
		Summary: "Remove saved credentials",
		Usage:   "fglpkg logout [--registry <name>]",
		Long: `FLAGS:
  --registry <name>        Remove credentials for a configured secondary
                           repository instead of the default GI registry.

Removes the saved credentials for the target registry from
~/.fglpkg/credentials.json.

NOTE:
  If FGLPKG_TOKEN is set, it authenticates the GI registry from the environment
  and cannot be removed by logout — unset FGLPKG_TOKEN to fully log out of GI.
`,
	},
	{
		Name:    "whoami",
		Summary: "Show current authenticated user",
		Usage:   "fglpkg whoami",
		Long: `Shows the authenticated user, partner, and scopes for the GI registry, plus
an Auth line reporting the credential source: "FGLPKG_TOKEN (environment
variable)" when the env var is set (it takes precedence), otherwise "stored
login".
`,
	},
	{
		Name:    "workspace",
		Aliases: []string{"ws"},
		Summary: "Manage monorepo workspaces",
		Usage:   "fglpkg workspace <init|add|list|info>",
		Long: `SUBCOMMANDS:
  init [members...]        Create fglpkg.workspace.json in the current directory
  add <path>               Add a member project to the workspace
  list                     List workspace members
  info                     Print a workspace summary
`,
	},
	{
		Name:    "registry",
		Summary: "Manage configured package repositories",
		Usage: "fglpkg registry list\n" +
			"fglpkg registry add <name> <url> [--type genero|artifactory] [--repo-key K]\n" +
			"                                 [--auth bearer|basic|apikey|anonymous] [--priority N]\n" +
			"                                 [--packages 'acme-*,foo-*'] [--project]\n" +
			"fglpkg registry remove <name> [--project]",
		Long: `SUBCOMMANDS:
  list                     Show configured repositories, priority, auth scheme, and login status
  add <name> <url>         Add a repository descriptor (defaults to type=artifactory)
  remove <name> (rm)       Remove a configured repository

<url> may be pasted with the Artifactory repo key still on the end
(https://acme.jfrog.io/artifactory/GeneroBDL): the key is split off the URL and
--repo-key is then optional.

FLAGS (add):
  --type <t>               genero | artifactory (default artifactory)
  --repo-key <k>           Artifactory generic-repo key; optional when the URL already carries it
  --auth <scheme>          bearer | basic | apikey | anonymous (default bearer)
  --priority <n>           Lower is tried first; unique. Defaults to max+1 when omitted
  --packages <globs>       Comma-separated name-scope allow-list (e.g. 'acme-*,foo-*')
  --project                Write to the project fglpkg.json instead of ~/.fglpkg/config.json

Repositories are configured via a "registries" array in fglpkg.json and/or
~/.fglpkg/config.json, alongside the built-in Genero Intelligence registry.
Lower "priority" is tried first; priorities must be unique. 'add'/'remove' edit
these files for you; credentials still flow through 'fglpkg login --registry'.

LOGIN column values:
  yes     credentials are stored for this repo (via 'fglpkg login')
  env     the GI registry is authenticated by the FGLPKG_TOKEN env var
  no      no usable credentials found
  anon    the repo's auth scheme is "anonymous" (no login required)

Sign in to a secondary repo with 'fglpkg login --registry <name>'. Set which
repo 'fglpkg publish' targets by default with a top-level "defaultRegistry" in
fglpkg.json (or the FGLPKG_PUBLISH_REGISTRY env var).
See specs/artifactory-secondary-repository.md.
`,
	},
	{
		Name:        "run",
		Summary:     "Run a script from an installed package",
		Args:        "<command>",
		Usage:       "fglpkg run <command> [-- args...]\nfglpkg run --list",
		Passthrough: true,
		Long: `FLAGS:
  --list, -l               List commands defined by installed packages

Runs a "bin" command from an installed package. Arguments after '--' (or
after the command name) are passed to the script unchanged.
`,
	},
	{
		Name:    "docs",
		Summary: "List or view package documentation",
		Args:    "<package>",
		Usage:   "fglpkg docs <package> [file]",
		Long: `With only a package name, lists its documentation files (or prints the doc
directly when the package declares exactly one). Pass a file name to print a
specific doc.
`,
	},
	{
		Name:    "version",
		Summary: "Print the fglpkg tool version",
		Usage:   "fglpkg version",
		Long: `Prints the fglpkg tool version. Equivalent to 'fglpkg --version' and
'fglpkg -v'. This command only reports the tool version and never touches
fglpkg.json — to change the package version, use 'fglpkg bump'.
`,
	},
	{
		Name:       "bump",
		Summary:    "Bump the package version in fglpkg.json",
		ListDetail: "\n(bump = patch|minor|major|prerelease|<semver>, add --git to tag)",
		Args:       "<bump>",
		Usage:      "fglpkg bump <patch|minor|major|prerelease|semver> [--git]",
		Long: `FLAGS:
  --git                    Stage, commit, and tag the new version

Updates the "version" field of fglpkg.json. Accepts a bump kind
(patch|minor|major|prerelease) or an explicit semver to set directly.
With --git, requires a clean working tree, then stages fglpkg.json,
commits, and creates a v<version> tag.
`,
	},
	{
		Name:    "self-update",
		Aliases: []string{"upgrade"},
		Summary: "Update fglpkg to the latest release",
		Usage:   "fglpkg self-update [--check] [--yes] [--force]",
		Long: `FLAGS:
  --check                  Report whether an update is available and exit;
                           never downloads or writes
  --yes, -y                Skip the confirmation prompt (for scripts)
  --force                  Re-install even if already on the latest version

Downloads the latest stable release for this OS/arch, verifies its Ed25519
release signature (chained to fglpkg's pinned root) and its SHA-256 checksum,
then atomically replaces the running executable. Latest-stable only — no
version pinning, pre-releases, or downgrade. Refuses on 'dev' builds and on
installs managed by a package manager such as Homebrew.
`,
	},
	{
		Name:    "help",
		Summary: "Show this help",
		Usage:   "fglpkg help [command]",
	},
}

// commandIndex maps every command name and alias to its registry entry.
// Built once at package init from the ordered commands slice.
var commandIndex = func() map[string]*command {
	idx := make(map[string]*command, len(commands))
	for i := range commands {
		c := &commands[i]
		idx[c.Name] = c
		for _, a := range c.Aliases {
			idx[a] = c
		}
	}
	return idx
}()

// isHelpFlag reports whether an argument is a help request.
func isHelpFlag(a string) bool {
	return a == "-h" || a == "--help"
}

// helpRequested reports whether args ask for this command's help. For
// passthrough commands (run, bdl) only the first argument is considered, so
// help flags meant for the invoked program are forwarded untouched.
func (c *command) helpRequested(args []string) bool {
	if c.Passthrough {
		return len(args) > 0 && isHelpFlag(args[0])
	}
	for _, a := range args {
		if isHelpFlag(a) {
			return true
		}
	}
	return false
}

// printCommandHelp renders a single command's help page: a header, its usage
// synopsis, and (when present) the detailed body.
func printCommandHelp(c *command) {
	// The header shows Summary only; ListDetail (parenthetical flag/arg hints)
	// is list-only and would duplicate the USAGE/FLAGS sections below.
	fmt.Printf("fglpkg %s - %s\n\n", c.Name, c.Summary)
	if len(c.Aliases) > 0 {
		fmt.Printf("ALIASES:\n  %s\n\n", strings.Join(c.Aliases, ", "))
	}
	fmt.Println("USAGE:")
	for _, line := range strings.Split(strings.TrimRight(c.Usage, "\n"), "\n") {
		fmt.Printf("  %s\n", line)
	}
	if c.Long != "" {
		fmt.Printf("\n%s", c.Long)
	}
}
