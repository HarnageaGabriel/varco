#!/bin/sh
# Test scripts/update-release-docs.sh against the fixtures in testdata/release-docs.
set -eu

here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo=$(CDPATH= cd -- "$here/.." && pwd)
script=$repo/scripts/update-release-docs.sh
fixtures=$here/testdata/release-docs

target=1.2.3
updated='.krew.yaml README.md docs/install.md docs/ci.md examples/github-actions/varco-compare.yml'

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
    fi
}

expect_same() {
    if cmp -s "$1" "$2"; then
        report 0 "$3"
    else
        report 1 "$3"
        if command -v diff >/dev/null 2>&1; then
            diff -u "$2" "$1" >&2 || true
        fi
    fi
}

run() {
    out=$1
    shift
    status=0
    sh "$script" "$@" > "$out" 2>&1 || status=$?
    return "$status"
}

have_git=0
if command -v git >/dev/null 2>&1; then
    have_git=1
fi

setup() {
    rm -rf "$tmp/repo"
    mkdir -p "$tmp/repo"
    cp -R "$fixtures/repo/." "$tmp/repo/"
    if [ "$have_git" -eq 1 ]; then
        git -C "$tmp/repo" init --quiet >/dev/null 2>&1 ||
            report 1 'the fixture repository can be initialized'
        git -C "$tmp/repo" -c core.autocrlf=false add -A
        git -C "$tmp/repo" -c core.autocrlf=false -c user.email=docs@example.com \
            -c user.name=docs commit --quiet -m fixture
    fi
}

# 1. The script rewrites the version strings and the Krew hashes.
setup
status=0
run "$tmp/out" --root "$tmp/repo" "$target" "$fixtures/checksums.txt" || status=$?
expect_ok 'the run succeeds' "$status"
for file in $updated; do
    expect_same "$tmp/repo/$file" "$fixtures/expected/$file" "$file matches the expected content"
    expect_contains "$tmp/out" "$file" "the summary lists $file"
done
expect_same "$tmp/repo/CHANGELOG.md" "$fixtures/repo/CHANGELOG.md" 'CHANGELOG.md is untouched'

# 2. A second run changes nothing.
status=0
run "$tmp/out2" --root "$tmp/repo" "$target" "$fixtures/checksums.txt" || status=$?
expect_ok 'the second run succeeds' "$status"
expect_contains "$tmp/out2" 'changed: none' 'the second run reports no changed file'
for file in $updated; do
    expect_same "$tmp/repo/$file" "$fixtures/expected/$file" "$file is unchanged after the second run"
done

# 3. A version that is not MAJOR.MINOR.PATCH is rejected.
setup
for bad in 1.2 v1.2.3 1.2.3.4 1.2.3-rc1 abc; do
    status=0
    run "$tmp/out" --root "$tmp/repo" "$bad" "$fixtures/checksums.txt" || status=$?
    expect_fail "the version $bad is rejected" "$status"
    expect_contains "$tmp/out" 'MAJOR.MINOR.PATCH' "the error for $bad names the expected format"
done
expect_same "$tmp/repo/.krew.yaml" "$fixtures/repo/.krew.yaml" '.krew.yaml is untouched after a bad version'
expect_same "$tmp/repo/README.md" "$fixtures/repo/README.md" 'README.md is untouched after a bad version'

# 4. An archive that is missing from the checksums file is a failure.
setup
status=0
run "$tmp/out" --root "$tmp/repo" "$target" "$fixtures/checksums-missing.txt" || status=$?
expect_fail 'the run fails when an archive is missing' "$status"
expect_contains "$tmp/out" 'varco_1.2.3_windows_amd64.zip' 'the error names the missing archive'
expect_same "$tmp/repo/.krew.yaml" "$fixtures/repo/.krew.yaml" '.krew.yaml is untouched after a missing archive'
expect_same "$tmp/repo/README.md" "$fixtures/repo/README.md" 'README.md is untouched after a missing archive'

# 5. Uncommitted changes in the files to update stop the run unless --force is given.
if [ "$have_git" -eq 1 ]; then
    setup
    printf 'dirty\n' >> "$tmp/repo/README.md"
    status=0
    run "$tmp/out" --root "$tmp/repo" "$target" "$fixtures/checksums.txt" || status=$?
    expect_fail 'the run fails on uncommitted changes' "$status"
    expect_contains "$tmp/out" 'uncommitted changes' 'the error names the uncommitted changes'
    status=0
    run "$tmp/out" --root "$tmp/repo" --force "$target" "$fixtures/checksums.txt" || status=$?
    expect_ok '--force overrides the uncommitted changes' "$status"
else
    printf 'skip: git is not installed, the uncommitted changes check is not tested\n'
fi

if [ "$failures" -ne 0 ]; then
    printf 'FAILED: %s of %s checks\n' "$failures" "$checks" >&2
    exit 1
fi
printf 'PASS: %s checks\n' "$checks"
