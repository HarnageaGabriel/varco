#!/bin/sh
# Rewrite the release version strings and the Krew hashes after a release is published.
#
# Usage: scripts/update-release-docs.sh [--root DIR] [--force] VERSION [CHECKSUMS_FILE]
#
# VERSION is MAJOR.MINOR.PATCH without the leading v, for example 0.1.2.
# CHECKSUMS_FILE is the checksums.txt of the release; without it the script
# downloads it with gh release download vVERSION --pattern checksums.txt.
#
# The script rewrites .krew.yaml, README.md, docs/install.md, docs/ci.md and
# examples/github-actions/varco-compare.yml, prints the changed files and
# changes nothing else: it never commits and never pushes.
#
# Exit codes: 0 success, 1 error, 2 usage error, 3 an archive is missing from
# the checksums file, 4 the files to update have uncommitted changes.

set -eu

prog=update-release-docs.sh

EX_ERROR=1
EX_USAGE=2
EX_MISSING=3
EX_DIRTY=4

krew_file=.krew.yaml
doc_files='README.md docs/install.md docs/ci.md examples/github-actions/varco-compare.yml'

fail() {
    code=$1
    shift
    printf '%s: %s\n' "$prog" "$*" >&2
    exit "$code"
}

usage() {
    printf 'Usage: %s [--root DIR] [--force] VERSION [CHECKSUMS_FILE]\n' "$prog" >&2
    exit "$EX_USAGE"
}

help() {
    printf 'Usage: %s [--root DIR] [--force] VERSION [CHECKSUMS_FILE]\n' "$prog"
    printf '\n'
    printf 'VERSION is MAJOR.MINOR.PATCH without the leading v, for example 0.1.2.\n'
    printf 'CHECKSUMS_FILE is the checksums.txt of the release; without it the script\n'
    printf 'downloads it with gh release download vVERSION --pattern checksums.txt.\n'
    printf '\n'
    printf 'The script rewrites .krew.yaml, README.md, docs/install.md, docs/ci.md and\n'
    printf 'examples/github-actions/varco-compare.yml and prints the changed files.\n'
}

root=
force=0
version=
checksums=
have_version=0
have_checksums=0

while [ "$#" -gt 0 ]; do
    case $1 in
    --root)
        [ "$#" -ge 2 ] || usage
        root=$2
        shift 2
        ;;
    --root=*)
        root=${1#--root=}
        shift
        ;;
    --force)
        force=1
        shift
        ;;
    -h|--help)
        help
        exit 0
        ;;
    --)
        shift
        while [ "$#" -gt 0 ]; do
            if [ "$have_version" -eq 0 ]; then
                version=$1
                have_version=1
            elif [ "$have_checksums" -eq 0 ]; then
                checksums=$1
                have_checksums=1
            else
                usage
            fi
            shift
        done
        break
        ;;
    -*)
        printf '%s: unknown option: %s\n' "$prog" "$1" >&2
        usage
        ;;
    *)
        if [ "$have_version" -eq 0 ]; then
            version=$1
            have_version=1
        elif [ "$have_checksums" -eq 0 ]; then
            checksums=$1
            have_checksums=1
        else
            usage
        fi
        shift
        ;;
    esac
done

[ "$have_version" -eq 1 ] || usage

if ! printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    fail "$EX_USAGE" "VERSION must look like MAJOR.MINOR.PATCH, for example 0.1.2: $version"
fi

if [ -n "$checksums" ]; then
    checksums_dir=$(CDPATH= cd -- "$(dirname -- "$checksums")" && pwd) ||
        fail "$EX_ERROR" "cannot read the directory of the checksums file: $checksums"
    checksums=$checksums_dir/$(basename -- "$checksums")
fi

if [ -z "$root" ]; then
    script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
    root=$(CDPATH= cd -- "$script_dir/.." && pwd)
fi
[ -d "$root" ] || fail "$EX_ERROR" "root directory not found: $root"
cd "$root"

[ -f "$krew_file" ] || fail "$EX_ERROR" "$krew_file not found in $root"
for doc in $doc_files; do
    [ -f "$doc" ] || fail "$EX_ERROR" "$doc not found in $root"
done

current=$(awk '
    /^spec:[ \t]*$/ { inspec = 1; next }
    inspec && /^[^ \t]/ { inspec = 0 }
    inspec && /^[ \t]+version:[ \t]*/ {
        value = $0
        sub(/^[ \t]+version:[ \t]*/, "", value)
        sub(/^v/, "", value)
        print value
        exit
    }
' "$krew_file")
[ -n "$current" ] || fail "$EX_ERROR" "cannot read the current version from $krew_file"
if ! printf '%s\n' "$current" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    fail "$EX_ERROR" "the current version in $krew_file is not MAJOR.MINOR.PATCH: $current"
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' 0 HUP INT TERM

if [ -z "$checksums" ]; then
    command -v gh >/dev/null 2>&1 ||
        fail "$EX_ERROR" 'gh is required to download checksums.txt; pass the file as the second argument'
    if ! gh release download "v$version" --pattern checksums.txt --dir "$tmpdir"; then
        fail "$EX_ERROR" "gh release download v$version failed"
    fi
    checksums=$tmpdir/checksums.txt
fi
[ -f "$checksums" ] || fail "$EX_ERROR" "checksums file not found: $checksums"

old=$current
new=$version
old_re=$(printf '%s' "$old" | sed 's/\./\\./g')

awk -v cfile="$checksums" -v oldv="$old" -v newv="$new" -v errfile="$tmpdir/awk.err" -v krewfile="$krew_file" '
    BEGIN {
        oldre = oldv
        gsub(/\./, "\\.", oldre)
        while ((getline line < cfile) > 0) {
            sub(/\r$/, "", line)
            hash = line
            sub(/[ \t].*$/, "", hash)
            name = line
            sub(/^[ \t]*[^ \t]+[ \t]+/, "", name)
            sub(/^\*/, "", name)
            sub(/[ \t]+$/, "", name)
            if (hash != "" && name != "") sum[tolower(name)] = tolower(hash)
        }
        close(cfile)
    }
    /^spec:[ \t]*$/ { inspec = 1; print; next }
    inspec && /^[^ \t]/ { inspec = 0 }
    inspec && /^[ \t]+version:[ \t]*/ {
        match($0, /^[ \t]+/)
        indent = substr($0, RSTART, RLENGTH)
        value = $0
        sub(/^[ \t]+version:[ \t]*/, "", value)
        prefix = (value ~ /^v/) ? "v" : ""
        print indent "version: " prefix newv
        next
    }
    /^[ \t]+uri:[ \t]*/ {
        match($0, /^[ \t]+/)
        indent = substr($0, RSTART, RLENGTH)
        uri = substr($0, RSTART + RLENGTH)
        newuri = uri
        gsub(/download\/v[0-9][0-9.]*\//, "download/v" newv "/", newuri)
        base = newuri
        sub(/^.*\//, "", base)
        newbase = base
        gsub(oldre, newv, newbase)
        sub(/\/[^/]*$/, "/" newbase, newuri)
        if (!(tolower(newbase) in sum)) {
            print "archive " newbase " is missing from the checksums file " cfile > errfile
            exit 3
        }
        pending = newbase
        print indent newuri
        next
    }
    /^[ \t]+sha256:[ \t]*/ {
        if (pending == "") {
            print "sha256 without a uri in " krewfile > errfile
            exit 3
        }
        match($0, /^[ \t]+/)
        print substr($0, RSTART, RLENGTH) "sha256: " sum[tolower(pending)]
        pending = ""
        next
    }
    { print }
' "$krew_file" > "$tmpdir/krew.yaml" || {
    status=$?
    if [ -s "$tmpdir/awk.err" ]; then
        cat "$tmpdir/awk.err" >&2
    fi
    [ "$status" -eq "$EX_MISSING" ] || status=$EX_ERROR
    exit "$status"
}

index=0
for doc in $doc_files; do
    index=$((index + 1))
    sed -e "s/v$old_re/v$new/g" -e "s/varco_${old_re}_/varco_${new}_/g" "$doc" > "$tmpdir/doc.$index"
done

todo=
if ! cmp -s "$tmpdir/krew.yaml" "$krew_file"; then
    todo=$krew_file
fi
index=0
for doc in $doc_files; do
    index=$((index + 1))
    if ! cmp -s "$tmpdir/doc.$index" "$doc"; then
        todo="$todo $doc"
    fi
done

if [ "$force" -eq 0 ] && [ -n "$todo" ] && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    dirty=$(git status --porcelain -- $todo)
    if [ -n "$dirty" ]; then
        printf '%s: the files to update have uncommitted changes:\n' "$prog" >&2
        printf '%s\n' "$dirty" >&2
        fail "$EX_DIRTY" 'commit them or rerun with --force'
    fi
fi

keep_mode() {
    if [ -x "$1" ]; then
        chmod +x "$2" 2>/dev/null || true
    fi
}

changed=
add_changed() {
    if [ -z "$changed" ]; then
        changed=$1
    else
        changed="$changed $1"
    fi
}

if ! cmp -s "$tmpdir/krew.yaml" "$krew_file"; then
    keep_mode "$krew_file" "$tmpdir/krew.yaml"
    mv "$tmpdir/krew.yaml" "$krew_file"
    add_changed "$krew_file"
fi

index=0
for doc in $doc_files; do
    index=$((index + 1))
    if ! cmp -s "$tmpdir/doc.$index" "$doc"; then
        keep_mode "$doc" "$tmpdir/doc.$index"
        mv "$tmpdir/doc.$index" "$doc"
        add_changed "$doc"
    fi
done

printf '%s: %s -> %s\n' "$prog" "$old" "$new"
if [ -z "$changed" ]; then
    printf 'changed: none\n'
else
    printf 'changed: %s\n' "$changed"
fi
