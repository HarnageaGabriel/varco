#!/bin/sh
# Test scripts/verify-reproducible.sh with a small fixture repository.
#
# The fixture is a tiny Go module with the shape of varco: a cmd/varco main
# package, an internal/version package and a .goreleaser.yaml with the build
# settings of the release. The test builds the fixture twice with the script,
# checks the report, checks that the temporary files are removed, compares the
# fixture with an archive that looks like a release, and checks that a build
# that differs is reported as not reproducible.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$here/.." && pwd)
script=$repo/scripts/verify-reproducible.sh

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' 0 HUP INT TERM
GIT_CEILING_DIRECTORIES=$tmp
export GIT_CEILING_DIRECTORIES

checks=0
failures=0

report() {
    checks=$((checks + 1))
    if [ "$1" -eq 0 ]; then
        printf 'ok: %s\n' "$2"
    else
        failures=$((failures + 1))
        printf 'FAIL: %s\n' "$2" >&2
    fi
}

expect_ok() {
    if [ "$2" -eq 0 ]; then
        report 0 "$1"
    else
        report 1 "$1 (exit $2)"
    fi
}

expect_fail() {
    if [ "$2" -ne 0 ]; then
        report 0 "$1"
    else
        report 1 "$1 (expected a failure)"
    fi
}

expect_contains() {
    if grep -q -- "$2" "$1"; then
        report 0 "$3"
    else
        report 1 "$3 (not found: $2)"
        cat "$1" >&2 || true
    fi
}

run() {
    out=$1
    shift
    status=0
    sh "$@" > "$out" 2>&1 || status=$?
    return "$status"
}

# 1. The script is valid POSIX shell.
status=0
sh -n "$script" || status=$?
expect_ok 'the script is valid shell' "$status"

# 2. The help text explains both modes.
status=0
run "$tmp/help" "$script" --help || status=$?
expect_ok 'the help text is printed' "$status"
expect_contains "$tmp/help" 'Usage:' 'the help text names the usage'

fixture=$tmp/fixture

setup_fixture() {
    rm -rf "$fixture"
    mkdir -p "$fixture/cmd/varco" "$fixture/internal/version" "$fixture/scripts"
    cat > "$fixture/go.mod" <<'EOF'
module example.com/fixture

go 1.21
EOF
    cat > "$fixture/cmd/varco/main.go" <<'EOF'
// Command fixture stands in for varco in the reproducibility test.
package main

import (
	"fmt"

	"example.com/fixture/internal/version"
)

func main() {
	fmt.Printf("varco %s (commit %s, built %s)\n", version.Version, version.Commit, version.Date)
}
EOF
    cat > "$fixture/internal/version/version.go" <<'EOF'
// Package version exposes build metadata set by the linker flags.
package version

// These values are replaced by the linker flags of the build.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)
EOF
    cat > "$fixture/.goreleaser.yaml" <<'EOF'
version: 2

project_name: varco

builds:
  - id: varco
    main: ./cmd/varco
    binary: varco
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - windows
    goarch:
      - amd64
    flags:
      - -trimpath
    ldflags:
      - >-
        -s -w
        -X example.com/fixture/internal/version.Version={{.Version}}
        -X example.com/fixture/internal/version.Commit={{.Commit}}
        -X example.com/fixture/internal/version.Date={{ .CommitDate }}
EOF
    cp "$script" "$fixture/scripts/verify-reproducible.sh"
    cat > "$fixture/.gitattributes" <<'EOF'
* text=auto eol=lf
EOF
    git -C "$fixture" init --quiet
    git -C "$fixture" config core.autocrlf false
    git -C "$fixture" -c user.email=test@example.com -c user.name=test add -A
    git -C "$fixture" -c user.email=test@example.com -c user.name=test commit --quiet -m fixture
    git -C "$fixture" tag v1.2.3
}

setup_fixture

if ! command -v go >/dev/null 2>&1; then
    printf 'skip: go is not installed, the build checks are not run\n'
    if [ "$failures" -ne 0 ]; then
        printf 'FAILED: %s of %s checks\n' "$failures" "$checks" >&2
        exit 1
    fi
    printf 'PASS: %s checks\n' "$checks"
    exit 0
fi

# 3. The script builds the fixture twice and reports identical binaries.
mkdir -p "$tmp/work"
probe=$(TMPDIR=$tmp/work mktemp -d)
if [ -n "$probe" ] && [ -d "$probe" ]; then
    rm -rf "$probe"
    TMPDIR=$tmp/work
    export TMPDIR
    status=0
    run "$tmp/local" "$fixture/scripts/verify-reproducible.sh" || status=$?
    expect_ok 'the two builds of the fixture are reproducible' "$status"
    expect_contains "$tmp/local" 'linux/amd64' 'the report names the linux target'
    expect_contains "$tmp/local" 'windows/amd64' 'the report names the windows target'
    expect_contains "$tmp/local" 'identical' 'the report says the binaries are identical'
    if [ -z "$(ls -A "$tmp/work" 2>/dev/null)" ]; then
        report 0 'the temporary files are removed'
    else
        report 1 'the temporary files are removed'
    fi
else
    printf 'skip: mktemp does not use TMPDIR, the build checks are not run\n'
fi

# 4. A released archive is compared with a rebuild of its tag.
mkdir -p "$tmp/release"
fixture_commit=$(git -C "$fixture" rev-parse v1.2.3^{commit})
fixture_ts=$(git -C "$fixture" show -s --format=%ct v1.2.3^{commit})
fixture_date=$(date -u -d "@$fixture_ts" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
    date -u -r "$fixture_ts" '+%Y-%m-%dT%H:%M:%SZ')
status=0
(
    cd "$fixture" || exit 1
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly GOTOOLCHAIN=local \
        go build -trimpath \
        -ldflags "-s -w -X example.com/fixture/internal/version.Version=1.2.3 -X example.com/fixture/internal/version.Commit=$fixture_commit -X example.com/fixture/internal/version.Date=$fixture_date" \
        -o "$tmp/release/varco" ./cmd/varco
) || status=$?
expect_ok 'the release archive of the fixture is built' "$status"
tar -czf "$tmp/varco_1.2.3_linux_amd64.tar.gz" -C "$tmp/release" varco
status=0
run "$tmp/archive" "$fixture/scripts/verify-reproducible.sh" "$tmp/varco_1.2.3_linux_amd64.tar.gz" || status=$?
expect_ok 'the released archive matches the rebuild of its tag' "$status"
expect_contains "$tmp/archive" 'identical' 'the report says the archive matches the rebuild'
if [ "$(git -C "$fixture" worktree list | wc -l)" -eq 1 ]; then
    report 0 'the git worktree of the comparison is removed'
else
    report 1 'the git worktree of the comparison is removed'
fi

# 5. A build that differs is reported as not reproducible.
# The copy builds the second target with another version, so the two builds
# of the fixture cannot be the same binary.
awk '
    {
        line = $0
        if (index(line, "$tmpdir/b") > 0) {
            needle = "\"" "$version" "\""
            replacement = "\"" "$version-tweaked" "\""
            at = index(line, needle)
            if (at > 0) {
                line = substr(line, 1, at - 1) replacement substr(line, at + length(needle))
            }
        }
        print line
    }
' "$script" > "$fixture/scripts/verify-reproducible-tweaked.sh"
status=0
run "$tmp/tweaked" "$fixture/scripts/verify-reproducible-tweaked.sh" || status=$?
expect_fail 'a build that differs is a failure' "$status"
expect_contains "$tmp/tweaked" 'DIFFERENT' 'the report says the binaries differ'

# 6. A directory that is not a checkout is a failure.
mkdir -p "$tmp/notarepo/scripts"
cp "$script" "$tmp/notarepo/scripts/verify-reproducible.sh"
status=0
run "$tmp/notarepo.out" "$tmp/notarepo/scripts/verify-reproducible.sh" || status=$?
expect_fail 'a directory that is not a checkout is a failure' "$status"
expect_contains "$tmp/notarepo.out" 'git does not work' 'the error names the missing checkout'

if [ "$failures" -ne 0 ]; then
    printf 'FAILED: %s of %s checks\n' "$failures" "$checks" >&2
    exit 1
fi
printf 'PASS: %s checks\n' "$checks"
