#!/usr/bin/env bash
# install.sh - build the agent-sync CLI from this checkout and run its
# "install" command to sync the global OpenCode agent files (AGENTS.md and
# agents/**).
#
# --repo is optional: when omitted, this checkout is the source and its
# committed "main" branch is validated and fetched (never a mutable working
# tree); an explicit --repo <url> overrides it for an external repository.
#
# Invoke it directly (./install.sh) even from zsh: the shebang selects bash.
# The binary is never copied: `go install` places it in GOBIN, or in the first
# GOPATH segment's bin when GOBIN is unset. The user PATH is only changed when
# --add-to-path is given; --yes is never forwarded, so the CLI keeps its own
# confirmation before it writes.

set -euo pipefail

fail() {
    printf 'error: %s\n' "$1" >&2
    exit "${2:-1}"
}

# Resolve the checkout from this script's own location, without leaving the
# caller's working directory.
script_path="${BASH_SOURCE[0]:-$0}"
script_dir="$(cd -- "$(dirname -- "$script_path")" >/dev/null 2>&1 && pwd -P)" || fail 'cannot resolve the script directory'
root_dir="$script_dir"

repo=''
repo_seen=0
add_to_path=0
dry_run=0

while [ "$#" -gt 0 ]; do
    case "$1" in
        --repo)
            if [ "$#" -lt 2 ]; then fail 'missing value for --repo' 2; fi
            repo="$2"
            repo_seen=1
            shift 2
            ;;
        --add-to-path)
            add_to_path=1
            shift
            ;;
        --dry-run)
            dry_run=1
            shift
            ;;
        *)
            fail "unknown argument: $1" 2
            ;;
    esac
done

command -v go >/dev/null 2>&1 || fail 'Go is required but was not found on PATH; install Go >= the version in go.mod and retry'
command -v git >/dev/null 2>&1 || fail 'Git is required but was not found on PATH; install Git and retry'

[ -f "$root_dir/go.mod" ] || fail "go.mod not found next to install.sh ($root_dir); run the script from the repository checkout"
[ -d "$root_dir/cmd/agent-sync" ] || fail "cmd/agent-sync not found under $root_dir; run the script from the repository checkout"

# Required Go version from go.mod (e.g. "go 1.27").
req_version="$(awk '$1 == "go" { print $2; exit }' "$root_dir/go.mod")"
case "$req_version" in
    ''|*[!0-9.]*) fail "could not read the required Go version from $root_dir/go.mod" ;;
esac
req_major="${req_version%%.*}"
req_rest="${req_version#*.}"
req_minor="${req_rest%%.*}"

# --repo is optional and never guessed dynamically. When it is given, it is
# used verbatim (an explicit empty value is still invalid). When it is omitted,
# this checkout is the source and the CLI fetches its committed "main" branch;
# the mutable working tree is never read implicitly.
if [ "$repo_seen" -eq 1 ]; then
    if [ -z "${repo//[[:space:]]/}" ]; then
        fail 'no repository given; pass --repo <url> for the repository that holds AGENTS.md and agents/**, or omit --repo to sync from this checkout' 2
    fi
else
    repo="$root_dir"

    # Validate the committed "main" branch before any build or write.
    git_root="$(git -C "$root_dir" rev-parse --show-toplevel 2>/dev/null)" || fail 'this checkout is not a git repository; the installer sources agent files from the committed "main" branch. Run "git init" (if needed) and commit AGENTS.md and agents/*.md to main, then retry, or pass --repo <url> for an external repository' 2
    [ -n "$git_root" ] || fail 'could not resolve the git top-level directory of this checkout; pass --repo <url> for an external repository' 2

    # The CLI is given this checkout's own root as its repo, so the git top-level
    # must be exactly this checkout, not an enclosing parent repository (a
    # subdirectory of a larger repository is not an immutable source on its own).
    # Resolve the physical path in a subshell (cd -P), so the caller's working
    # directory is never changed, and compare it to the already-physical root_dir.
    git_root_phys="$(cd -- "$git_root" >/dev/null 2>&1 && pwd -P)" || fail "could not resolve the git top-level directory '$git_root'; pass --repo <url> for an external repository" 2
    [ "$git_root_phys" = "$root_dir" ] || fail "git top-level '$git_root' is not this checkout ('$root_dir'); the installer sources the repository that owns install.sh, so run the script from the repository root (or pass --repo <url> for an external repository)" 2

    git -C "$git_root" rev-parse --verify --quiet refs/heads/main >/dev/null 2>&1 || fail 'this git repository has no committed "main" branch; commit the imported agents to main first (AGENTS.md and agents/*.md), then retry' 2

    ls_out="$(git -C "$git_root" ls-tree -r --name-only refs/heads/main -- agents)" || fail 'could not list the committed agents/ tree on the "main" branch; verify the repository is intact and retry' 2

    eligible=0
    while IFS= read -r rel; do
        case "$rel" in
            agents/*.md) eligible=$((eligible + 1)) ;;
        esac
    done <<EOF
$ls_out
EOF
    [ "$eligible" -gt 0 ] || fail 'the committed "main" branch has no eligible agent markdown under agents/; commit the imported agents (agents/*.md) to main first, then retry' 2

    branch="$(git -C "$git_root" rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
    if [ -n "$branch" ] && [ "$branch" != "main" ]; then
        printf 'note: current branch is "%s"; the CLI sources the committed "main" branch and this checkout is left unchanged\n' "$branch"
    fi
fi

# Use only the locally installed toolchain for every child process: never let
# Go download another one and never persist it with `go env -w`. The export is
# scoped to this script's process tree.
export GOTOOLCHAIN=local

go_version_output="$(go version)" || fail "go version failed (exit $?)"
case "$go_version_output" in
    'go version go'*) ;;
    *) fail "unsupported Go version ($go_version_output): a stable go1.x is required; install a supported Go and retry (no automatic install)" ;;
esac
installed="${go_version_output#go version go}"
installed="${installed%% *}"
case "$installed" in
    ''|*[!0-9.]*) fail "unsupported Go version ($go_version_output): a stable go1.x is required; install a supported Go and retry (no automatic install)" ;;
esac
v_major="${installed%%.*}"
v_rest="${installed#*.}"
v_minor="${v_rest%%.*}"
if [ "$v_major" -lt "$req_major" ] || { [ "$v_major" -eq "$req_major" ] && [ "$v_minor" -lt "$req_minor" ]; }; then
    fail "Go $v_major.$v_minor is too old for go.mod (requires $req_major.$req_minor); upgrade Go and retry (no automatic install)"
fi

# Bin directory used by `go install`: GOBIN when absolute, otherwise the first
# GOPATH segment plus bin.
gobin="$(go env GOBIN || true)"
case "$gobin" in
    /*)
        bin_dir="$gobin"
        ;;
    *)
        gopath="$(go env GOPATH || true)"
        [ -n "$gopath" ] || fail 'cannot determine GOBIN or GOPATH from Go; set GOBIN or GOPATH and retry'
        first_gopath="${gopath%%:*}"
        [ -n "$first_gopath" ] || fail "cannot determine the first GOPATH segment from '$gopath'"
        bin_dir="$first_gopath/bin"
        ;;
esac
exe="$bin_dir/agent-sync"

if [ "$dry_run" -eq 1 ]; then
    printf 'dry-run: source root: %s\n' "$root_dir"
    if [ "$repo_seen" -eq 0 ]; then
        printf 'dry-run: source checkout: %s (committed main, %s eligible agent file(s))\n' "$git_root" "$eligible"
    fi
    printf 'dry-run: go: %s (required >= %s.%s)\n' "$go_version_output" "$req_major" "$req_minor"
    printf 'dry-run: binary directory: %s\n' "$bin_dir"
    printf 'dry-run: build: go install ./cmd/agent-sync (cwd: %s)\n' "$root_dir"
    printf 'dry-run: command: %s install --repo %s' "$exe" "$repo"
    [ "$add_to_path" -eq 1 ] && printf ' --add-to-path'
    printf ' --shell zsh\n'
    printf 'dry-run: target: %s/.config/opencode\n' "$HOME"
    printf 'dry-run: state:  %s/.local/state/agent-sync\n' "$HOME"
    printf 'dry-run: no files were written; PATH and profiles were not touched\n'
    exit 0
fi

( cd "$root_dir" && go install ./cmd/agent-sync ) || fail 'go install ./cmd/agent-sync failed'

if [ ! -e "$exe" ]; then
    fail "go install succeeded but $exe was not found; check GOBIN/GOPATH"
fi
printf 'binary directory: %s\n' "$bin_dir"

# The exact CLI argument vector. --add-to-path is the only PATH consent; the
# shell is fixed to zsh for this Fedora/Zsh wrapper and --yes is never added.
set -- install --repo "$repo"
if [ "$add_to_path" -eq 1 ]; then
    set -- "$@" --add-to-path
fi
set -- "$@" --shell zsh

"$exe" "$@" || exit $?
exit 0
