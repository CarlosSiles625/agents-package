#!/usr/bin/env pwsh
# install.ps1 - build the agent-sync CLI from this checkout and run its
# "install" command to sync the global OpenCode agent files (AGENTS.md and
# agents/**).
#
# -Repo is optional: when omitted, this checkout is the source and its committed
# "main" branch is validated and fetched (never a mutable working tree); an
# explicit -Repo <url> overrides it for an external repository.
#
# The binary is never copied: `go install` places it in GOBIN, or in the first
# GOPATH segment's bin when GOBIN is unset. The user PATH is only changed when
# -AddToPath is given; --yes is never forwarded, so the CLI keeps its own
# confirmation before it writes.

[CmdletBinding()]
param(
    [string]$Repo,
    [switch]$AddToPath,
    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'
if (Get-Variable -Name PSNativeCommandUseErrorActionPreference -ErrorAction SilentlyContinue) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$script:ExitCode = 0
$repoBound = $PSBoundParameters.ContainsKey('Repo')

function Fail([string]$Message, [int]$Code = 1) {
    $script:ExitCode = $Code
    throw $Message
}

# Run git with an explicit argument vector (never through a shell) and return
# its exit code and stdout lines. stderr is suppressed; the caller branches on
# Code. Git is always invoked as `git -C <dir> ...`.
function Invoke-GitCapture([string[]]$GitArgs) {
    $output = & git @GitArgs 2>$null
    return [pscustomobject]@{ Output = @($output); Code = $LASTEXITCODE }
}

function New-BlockedPlan([string]$Message) {
    [pscustomobject]@{ Blocker = $Message; GitRoot = $null; Eligible = @(); Branch = '' }
}

# Validate the default source (this checkout). It must be a git repository whose
# committed "main" branch contains eligible agent markdown. This never copies a
# mutable working tree: the CLI itself fetches the immutable "main" commit, and
# here we only prove that such a commit exists, before any build or write.
function Get-DefaultAgentSourcePlan([string]$StartDir) {
    $top = Invoke-GitCapture @('-C', $StartDir, 'rev-parse', '--show-toplevel')
    if ($top.Code -ne 0) {
        return New-BlockedPlan 'this checkout is not a git repository; the installer sources agent files from the committed "main" branch. Run "git init" (if needed) and commit AGENTS.md and agents/*.md to main, then retry, or pass -Repo <url> for an external repository'
    }
    $gitRoot = ''
    foreach ($line in $top.Output) { if ("$line".Trim() -ne '') { $gitRoot = "$line".Trim(); break } }
    if ($gitRoot -eq '') {
        return New-BlockedPlan 'could not resolve the git top-level directory of this checkout; pass -Repo <url> for an external repository'
    }
    # The CLI is given this checkout's own root as its repo, so the git top-level
    # must be exactly this checkout, not an enclosing parent repository (a
    # subdirectory of a larger repository is not an immutable source on its own).
    # Compare normalized absolute paths without dereferencing symlinks; Windows
    # path comparison is case-insensitive.
    $normGitRoot = [System.IO.Path]::GetFullPath($gitRoot).TrimEnd([char[]]@('\', '/'))
    $normStartDir = [System.IO.Path]::GetFullPath($StartDir).TrimEnd([char[]]@('\', '/'))
    if (-not [string]::Equals($normGitRoot, $normStartDir, [System.StringComparison]::OrdinalIgnoreCase)) {
        return New-BlockedPlan "git top-level '$gitRoot' is not this checkout ('$StartDir'); the installer sources the repository that owns install.ps1, so run the script from the repository root (or pass -Repo <url> for an external repository)"
    }
    $main = Invoke-GitCapture @('-C', $gitRoot, 'rev-parse', '--verify', '--quiet', 'refs/heads/main')
    if ($main.Code -ne 0) {
        return New-BlockedPlan 'this git repository has no committed "main" branch; commit the imported agents to main first (AGENTS.md and agents/*.md), then retry'
    }
    $tree = Invoke-GitCapture @('-C', $gitRoot, 'ls-tree', '-r', '--name-only', 'refs/heads/main', '--', 'agents')
    if ($tree.Code -ne 0) {
        return New-BlockedPlan 'could not list the committed agents/ tree on the "main" branch; verify the repository is intact and retry'
    }
    $eligible = New-Object System.Collections.Generic.List[string]
    foreach ($line in $tree.Output) {
        $rel = "$line".Trim()
        if ($rel -eq '') { continue }
        if ($rel -cnotlike 'agents/*') { continue }
        if ($rel -cnotlike '*.md') { continue }
        [void]$eligible.Add($rel)
    }
    if ($eligible.Count -eq 0) {
        return New-BlockedPlan 'the committed "main" branch has no eligible agent markdown under agents/; commit the imported agents (agents/*.md) to main first, then retry'
    }
    $branch = ''
    $head = Invoke-GitCapture @('-C', $gitRoot, 'rev-parse', '--abbrev-ref', 'HEAD')
    if ($head.Code -eq 0) { foreach ($line in $head.Output) { if ("$line".Trim() -ne '') { $branch = "$line".Trim(); break } } }
    [pscustomobject]@{ Blocker = $null; GitRoot = $gitRoot; Eligible = @($eligible); Branch = $branch }
}

$prevToolchain = [Environment]::GetEnvironmentVariable('GOTOOLCHAIN', 'Process')
$pushed = $false

try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Fail 'Go is required but was not found on PATH; install Go >= the version in go.mod and retry'
    }
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
        Fail 'Git is required but was not found on PATH; install Git and retry'
    }

    # Resolve the checkout from this script's own location, never from the
    # caller's working directory.
    $root = $PSScriptRoot
    if (-not $root) { $root = (Get-Location).Path }

    $goMod = Join-Path $root 'go.mod'
    if (-not (Test-Path -LiteralPath $goMod -PathType Leaf)) {
        Fail "go.mod not found next to install.ps1 ($root); run the script from the repository checkout"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $root 'cmd\agent-sync') -PathType Container)) {
        Fail "cmd/agent-sync not found under $root; run the script from the repository checkout"
    }

    # Required Go version from go.mod (e.g. "go 1.27").
    $reqMajor = -1
    $reqMinor = -1
    foreach ($line in Get-Content -LiteralPath $goMod) {
        if ($line -match '^\s*go\s+(\d+)\.(\d+)') {
            $reqMajor = [int]$Matches[1]
            $reqMinor = [int]$Matches[2]
            break
        }
    }
    if ($reqMajor -lt 0) { Fail "could not read the required Go version from $goMod" }

    # -Repo is optional and never guessed dynamically. When it is given, it is
    # used verbatim (an explicit empty value is still invalid). When it is
    # omitted, this checkout is the source and the CLI fetches its committed
    # "main" branch; the mutable working tree is never read implicitly.
    if ($repoBound) {
        if (-not $Repo -or $Repo.Trim().Length -eq 0) {
            Fail 'no repository given; pass -Repo <url> for the repository that holds AGENTS.md and agents/**, or omit -Repo to sync from this checkout' 2
        }
    } else {
        $Repo = $root
    }

    # Native argv safety: PowerShell 5.1 re-serializes each element of a native
    # command line, so a raw double quote in the received value can split into
    # extra CLI flags (e.g. --yes) when the CLI is invoked; CR/LF/NUL are
    # rejected for the same reason. A Windows path cannot contain a literal
    # quote, so encode the URL instead. This checks the value as received; with
    # -File, PowerShell may already have transformed inbound quotes.
    if ($Repo -match '["\r\n\x00]') {
        Fail 'the repository URL cannot contain a double quote, CR, LF or NUL; a native Windows path cannot hold a literal quote, so percent-encode the URL (or use a path without one) and retry'
    }

    # Default source (this checkout): validate the committed "main" branch
    # before any build or write. Explicit -Repo skips this; an external
    # repository is the CLI's own responsibility.
    $sourcePlan = $null
    if (-not $repoBound) {
        $sourcePlan = Get-DefaultAgentSourcePlan $root
        if ($sourcePlan.Blocker) { Fail $sourcePlan.Blocker 2 }
        if ($sourcePlan.Branch -and $sourcePlan.Branch -ne 'main') {
            Write-Output "note: current branch is '$($sourcePlan.Branch)'; the CLI sources the committed 'main' branch and this checkout is left unchanged"
        }
    }

    # Use only the locally installed toolchain for every child process: never
    # let Go download another one and never persist it with `go env -w`.
    $env:GOTOOLCHAIN = 'local'

    $versionOutput = ((& go version 2>&1) | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) { Fail "go version failed (exit $LASTEXITCODE): $versionOutput" }
    if ($versionOutput -notmatch '^go version go(\d+)\.(\d+)(?:\.(\d+))?\s') {
        Fail "unsupported Go version ($versionOutput): a stable go1.x is required; install a supported Go and retry (no automatic install)"
    }
    $vMajor = [int]$Matches[1]
    $vMinor = [int]$Matches[2]
    if ($vMajor -lt $reqMajor -or ($vMajor -eq $reqMajor -and $vMinor -lt $reqMinor)) {
        Fail "Go $vMajor.$vMinor is too old for go.mod (requires $reqMajor.$reqMinor); upgrade Go and retry (no automatic install)"
    }

    # Bin directory used by `go install`: GOBIN when set and absolute, otherwise
    # the first GOPATH segment plus bin.
    $binDir = ''
    $gobin = ((& go env GOBIN 2>&1) | Out-String).Trim()
    if ($gobin -and [System.IO.Path]::IsPathRooted($gobin)) {
        $binDir = $gobin
    } else {
        $gopath = ((& go env GOPATH 2>&1) | Out-String).Trim()
        if (-not $gopath) { Fail 'cannot determine GOBIN or GOPATH from Go; set GOBIN or GOPATH and retry' }
        $firstGopath = ($gopath -split ';')[0].Trim()
        if (-not $firstGopath) { Fail "cannot determine the first GOPATH segment from '$gopath'" }
        $binDir = Join-Path $firstGopath 'bin'
    }
    $exe = Join-Path $binDir 'agent-sync.exe'

    # The exact CLI argument vector. -AddToPath is the only PATH consent; there
    # is no --shell on Windows and no --yes or --replace is ever added.
    $cliArgs = @('install', '--repo', $Repo)
    if ($AddToPath) { $cliArgs += '--add-to-path' }

    if ($DryRun) {
        $homeDir = $env:USERPROFILE
        if (-not $homeDir) { $homeDir = [Environment]::GetFolderPath('UserProfile') }
        Write-Output "dry-run: source root: $root"
        if ($sourcePlan) {
            Write-Output "dry-run: source checkout: $($sourcePlan.GitRoot) (committed main, $($sourcePlan.Eligible.Count) eligible agent file(s))"
        }
        Write-Output "dry-run: go: $versionOutput (required >= $reqMajor.$reqMinor)"
        Write-Output "dry-run: binary directory: $binDir"
        Write-Output "dry-run: build: go install ./cmd/agent-sync (cwd: $root)"
        Write-Output "dry-run: command: $exe $($cliArgs -join ' ')"
        Write-Output "dry-run: target: $(Join-Path $homeDir '.config\opencode')"
        Write-Output "dry-run: state:  $(Join-Path $homeDir '.local\state\agent-sync')"
        Write-Output 'dry-run: no files were written; PATH, registry and profiles were not touched'
    } else {
        Push-Location -LiteralPath $root
        $pushed = $true
        & go install ./cmd/agent-sync
        $installCode = $LASTEXITCODE
        Pop-Location
        $pushed = $false

        if ($installCode -ne 0) { Fail "go install ./cmd/agent-sync failed (exit $installCode)" }
        if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
            Fail "go install succeeded but $exe was not found; check GOBIN/GOPATH"
        }
        Write-Output "binary directory: $binDir"

        # Native argument array: each element is passed verbatim, so a repo with
        # spaces, quotes, $ or ; reaches the CLI unchanged.
        & $exe @cliArgs
        $script:ExitCode = $LASTEXITCODE
    }
} catch {
    [Console]::Error.WriteLine("error: $($_.Exception.Message)")
    if ($script:ExitCode -eq 0) { $script:ExitCode = 1 }
} finally {
    if ($pushed) { Pop-Location }
    if ($null -eq $prevToolchain) { Remove-Item Env:GOTOOLCHAIN -ErrorAction SilentlyContinue }
    else { $env:GOTOOLCHAIN = $prevToolchain }
}

exit $script:ExitCode
