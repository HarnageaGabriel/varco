#!/bin/sh
# Verify that the varco release binaries are reproducible.
#
# Usage: scripts/verify-reproducible.sh [ARCHIVE]
#
# Without an argument the script builds the linux/amd64 and the windows/amd64
# binary twice from the current checkout, in two separate temporary output
# directories, with the flags of .goreleaser.yaml, and compares the SHA-256 of
# the two builds of every target. It exits 0 when every target is reproducible
# and 1 when a target differs.
#
# With an argument the argument is the path of a released archive
# (varco_VERSION_os_arch.tar.gz or varco_VERSION_os_arch.zip). The script
# extracts the binary from the archive, rebuilds the same version from the
# matching tag in a temporary git worktree, and compares the SHA-256 of the
# two binaries.
#
# The build flags are read from .goreleaser.yaml, so the script follows the
# release configuration instead of repeating it. The version, commit and date
# of a build are the ones of the checked out commit: the version of the
# nearest tag (with the -SNAPSHOT-<short commit> suffix of a GoReleaser
# snapshot when the commit is not tagged), the full commit SHA and the commit
# timestamp in UTC.
#
# The script needs git, go and a SHA-256 tool. It needs no network except for
# git, and only when the release tag is missing from the local clone.
#
# Exit codes: 0 the binaries are reproducible, 1 they differ or the check could
# not be run, 2 the arguments are wrong.

set -eu

prog=verify-reproducible.sh

EX_OK=0
EX_FAIL=1
EX_USAGE=2

targets='linux/amd64 windows/amd64'

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
cd "$repo_root"

tmpdir=
worktree=
build_dir=$repo_root

fail() {
    printf '%s: %s\n' "$prog" "$*" >&2
    exit "$EX_FAIL"
}

note() {
    printf '%s: %s\n' "$prog" "$*"
}

usage() {
    printf 'Usage: %s [ARCHIVE]\n' "$prog" >&2
    printf 'ARCHIVE is a released archive, for example varco_0.1.1_linux_amd64.tar.gz.\n' >&2
    exit "$EX_USAGE"
}

help() {
    printf 'Usage: %s [ARCHIVE]\n' "$prog"
    printf '\n'
    printf 'Without an argument the script builds the linux/amd64 and the windows/amd64\n'
    printf 'binary twice from the current checkout and compares the SHA-256 of both\n'
    printf 'builds of every target.\n'
    printf '\n'
    printf 'With an argument the script extracts the binary of a released archive,\n'
    printf 'rebuilds the same version from the matching tag in a temporary git\n'
    printf 'worktree, and compares the SHA-256 of both binaries.\n'
    printf '\n'
    printf 'Exit codes: 0 the binaries are reproducible, 1 they differ, 2 bad arguments.\n'
}

cleanup() {
    if [ -n "$worktree" ]; then
        git_use_dir "$repo_root" >/dev/null 2>&1 || true
        git worktree remove --force "$worktree" >/dev/null 2>&1 || true
        git worktree prune >/dev/null 2>&1 || true
    fi
    if [ -n "$tmpdir" ] && [ -d "$tmpdir" ]; then
        rm -rf "$tmpdir"
    fi
}

trap cleanup 0
trap 'exit 130' HUP INT TERM

# ------------------------------------------------------------------ tools --

sha256_of() {
    sha_file=$1
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$sha_file" | awk '{print $1; exit}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$sha_file" | awk '{print $1; exit}'
    elif command -v openssl >/dev/null 2>&1; then
        openssl dgst -sha256 "$sha_file" | awk '{print $NF; exit}'
    else
        fail 'no SHA-256 tool found: install sha256sum, shasum or openssl'
    fi
}

host_os() {
    case $(uname -s 2>/dev/null || echo unknown) in
    Linux) echo linux ;;
    Darwin) echo darwin ;;
    MINGW*|MSYS*|CYGWIN*|Windows*) echo windows ;;
    *) echo unknown ;;
    esac
}

# git_use_dir DIR makes git work in DIR.
#
# A checkout that is a Windows worktree of a repository on another drive has a
# .git file with a Windows path. Git from another system (WSL) cannot read it,
# so the path is translated and passed through GIT_DIR and GIT_WORK_TREE.
git_use_dir() {
    git_target_dir=$1
    unset GIT_DIR GIT_WORK_TREE 2>/dev/null || true
    if (cd "$git_target_dir" && git rev-parse --git-dir >/dev/null 2>&1); then
        return 0
    fi
    if [ -f "$git_target_dir/.git" ]; then
        git_link=$(sed -n 's/^gitdir: //p' "$git_target_dir/.git" | head -1)
        case $git_link in
        [A-Za-z]:/*)
            git_link=/mnt/$(printf '%s' "$git_link" | cut -c1 | tr 'A-Z' 'a-z')/$(printf '%s' "$git_link" | cut -c3-)
            ;;
        esac
        if [ -d "$git_link" ]; then
            GIT_DIR=$git_link
            GIT_WORK_TREE=$git_target_dir
            export GIT_DIR GIT_WORK_TREE
            if (cd "$git_target_dir" && git rev-parse --git-dir >/dev/null 2>&1); then
                return 0
            fi
        fi
    fi
    return 1
}

# ------------------------------------------------- release configuration --

# gr_scalar KEY FILE prints a scalar key of the first build of .goreleaser.yaml.
gr_scalar() {
    awk -v key="$1" '
        /^builds:[ \t]*$/ { inbuilds = 1; next }
        inbuilds && /^[ \t]*-[ \t]*id:/ { if (seen) exit; seen = 1; next }
        inbuilds && seen && $0 ~ ("^[ \t]+" key ":[ \t]*") {
            line = $0
            sub("^[ \t]+" key ":[ \t]*", "", line)
            print line
            exit
        }
    ' "$2"
}

# gr_list KEY FILE prints the items of a list key of the first build.
gr_list() {
    awk -v key="$1" '
        /^builds:[ \t]*$/ { inbuilds = 1; next }
        inbuilds && /^[ \t]*-[ \t]*id:/ { if (seen) exit; seen = 1; next }
        inbuilds && seen && $0 ~ ("^[ \t]+" key ":[ \t]*$") { inlist = 1; next }
        inlist && /^[ \t]*-[ \t]*/ {
            line = $0
            sub(/^[ \t]*-[ \t]*/, "", line)
            print line
            next
        }
        inlist { exit }
    ' "$2"
}

# gr_ldflags FILE prints the linker flags of the first build as one line.
gr_ldflags() {
    awk '
        /^builds:[ \t]*$/ { inbuilds = 1; next }
        inbuilds && /^[ \t]*-[ \t]*id:/ { if (seen) exit; seen = 1; next }
        inbuilds && seen && /^[ \t]+ldflags:[ \t]*$/ { inld = 1; next }
        inld && /^[ \t]*-[ \t]*[>|]/ { inblock = 1; next }
        inld && inblock && /^       / {
            line = $0
            sub(/^[ \t]+/, "", line)
            printf "%s%s", (out == "" ? "" : " "), line
            out = out " " line
            next
        }
        inld && /^[ \t]*-[ \t]*/ {
            line = $0
            sub(/^[ \t]*-[ \t]*/, "", line)
            printf "%s%s", (out == "" ? "" : " "), line
            out = out " " line
            next
        }
        inld { exit }
    ' "$1"
}

# render_ldflags VERSION COMMIT DATE replaces the GoReleaser template
# variables of the release linker flags with the values of the build.
render_ldflags() {
    render_version=$1
    render_commit=$2
    render_date=$3
    printf '%s' "$release_ldflags" | awk -v v="$render_version" -v c="$render_commit" -v d="$render_date" '
        {
            gsub(/[{][{][[:space:]]*\.CommitDate[[:space:]]*[}][}]/, d)
            gsub(/[{][{][[:space:]]*\.Version[[:space:]]*[}][}]/, v)
            gsub(/[{][{][[:space:]]*\.Commit[[:space:]]*[}][}]/, c)
            gsub(/[{][{][[:space:]]*\.Date[[:space:]]*[}][}]/, d)
            print
        }
    '
}

# read_release_config DIR reads the build settings of the release configuration
# of DIR into the release_* variables.
read_release_config() {
    config_dir=$1
    release_config=$config_dir/.goreleaser.yaml
    [ -f "$release_config" ] || fail "the release configuration is not found: $release_config"

    release_main=$(gr_scalar main "$release_config")
    release_binary=$(gr_scalar binary "$release_config")
    release_flags=$(gr_list flags "$release_config" | tr '\n' ' ')
    release_env=$(gr_list env "$release_config" | tr '\n' ' ')
    release_ldflags=$(gr_ldflags "$release_config")

    [ -n "$release_main" ] || fail "cannot read the main package from $release_config"
    [ -n "$release_binary" ] || fail "cannot read the binary name from $release_config"
    [ -n "$release_ldflags" ] || fail "cannot read the linker flags from $release_config"
    case $release_env in
    *'{{'*)
        fail "the release environment uses a template this script does not know: $release_env"
        ;;
    esac
    case $release_env in
    *GOFLAGS*)
        note 'warning: the release configuration sets GOFLAGS; this script uses GOFLAGS=-mod=readonly'
        ;;
    esac
}

# ------------------------------------------------------------------ build --

# binary_name TARGET prints the file name of the binary of TARGET.
binary_name() {
    case ${1%%/*} in
    windows) printf '%s.exe' "$release_binary" ;;
    *) printf '%s' "$release_binary" ;;
    esac
}

# build_binary OUT_DIR TARGET VERSION COMMIT DATE builds TARGET and writes the
# binary into OUT_DIR.
build_binary() {
    build_out_dir=$1
    build_target=$2
    build_version=$3
    build_commit=$4
    build_date=$5

    build_os=${build_target%%/*}
    build_arch=${build_target#*/}
    build_out=$build_out_dir/$(binary_name "$build_target")
    build_ldflags=$(render_ldflags "$build_version" "$build_commit" "$build_date")
    case $build_ldflags in
    *'{{'*)
        fail "the release linker flags use a template this script does not know: $release_ldflags"
        ;;
    esac

    mkdir -p "$build_out_dir"
    (
        cd "$build_dir" || exit 1
        for release_entry in $release_env; do
            export "$release_entry"
        done
        export GOOS=$build_os
        export GOARCH=$build_arch
        export GOFLAGS=-mod=readonly
        export GOTOOLCHAIN=local
        # The flags of the release configuration are word split on purpose.
        go build $release_flags -ldflags "$build_ldflags" -o "$build_out" "$release_main"
    ) || fail "the build of $build_target failed"
    [ -f "$build_out" ] || fail "the build of $build_target produced no binary"
}

# commit_date REF prints the commit timestamp of REF in UTC, RFC 3339.
#
# The timestamp is read as the number of seconds since the epoch and formatted
# by date: git formats the commit date in the timezone of the machine, and
# Git for Windows ignores TZ, so the value of TZ=UTC would not be the UTC
# time of the commit. The epoch is the same in every timezone.
commit_date() {
    commit_ts=$(git show -s --format=%ct "$1^{commit}")
    date -u -d "@$commit_ts" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null ||
        date -u -r "$commit_ts" '+%Y-%m-%dT%H:%M:%SZ'
}

# ----------------------------------------------------------------- checks --

# check_toolchain DIR prints the local toolchain and the one of the release.
check_toolchain() {
    toolchain_dir=$1
    toolchain_local=$(go env GOVERSION 2>/dev/null || echo unknown)
    toolchain_required=$(awk '/^go [0-9]/ { print $2; exit }' "$toolchain_dir/go.mod" 2>/dev/null || true)
    toolchain_workflow=unknown
    if [ -f "$toolchain_dir/.github/workflows/release.yml" ]; then
        toolchain_workflow=$(grep -E 'go-version(-file)?:' "$toolchain_dir/.github/workflows/release.yml" |
            head -1 | sed 's/^[ \t]*//')
    fi
    printf 'toolchain: local %s, release %s (go %s in go.mod)\n' \
        "$toolchain_local" "$toolchain_workflow" "${toolchain_required:-unknown}"
    if [ -n "$toolchain_required" ]; then
        toolchain_series=$(printf '%s' "$toolchain_local" | sed 's/^go//; s/\..*//')
        toolchain_release_series=$(printf '%s' "$toolchain_required" | sed 's/\..*//')
        if [ "$toolchain_series" != "$toolchain_release_series" ]; then
            printf 'warning: the local toolchain %s is not the %s.x series of the release, the binaries can differ\n' \
                "$toolchain_local" "$toolchain_required"
        fi
    fi
}

# detect_built_date BINARY TARGET prints the build date the binary was built
# with. It is empty when the binary cannot run on this host.
detect_built_date() {
    detect_binary=$1
    detect_target=$2
    detect_os=${detect_target%%/*}
    if [ "$detect_os" != "$(host_os)" ]; then
        return 0
    fi
    detect_out=$("$detect_binary" version --json 2>/dev/null || true)
    printf '%s' "$detect_out" | sed -n 's/.*"date":"\([^"]*\)".*/\1/p'
}

# has_vcs_info BINARY reports whether the binary carries the version control
# information of the build. The go command does not stamp it in a linked git
# worktree, and the release binaries do carry it.
has_vcs_info() {
    go version -m "$1" 2>/dev/null | grep -q 'vcs=git'
}

# ----------------------------------------------------------------- report --

table_header() {
    printf '%-13s %-64s %-64s %s\n' TARGET 'BUILD 1' 'BUILD 2' RESULT
}

# --------------------------------------------------------------- commands --

# check_local builds every target twice and compares the two builds.
check_local() {
    git_use_dir "$repo_root" || fail "git does not work in $repo_root"
    read_release_config "$repo_root"

    version=$(git describe --tags --abbrev=0 HEAD 2>/dev/null || true)
    short_commit=$(git rev-parse --short=7 HEAD)
    case $version in
    v*) version=${version#v} ;;
    *) version=0.0.0 ;;
    esac
    if [ -z "$(git tag --points-at HEAD)" ]; then
        version=$version-SNAPSHOT-$short_commit
    fi
    commit=$(git rev-parse HEAD)
    date=$(commit_date HEAD)

    printf 'checkout:  %s\n' "$repo_root"
    printf 'commit:    %s\n' "$commit"
    printf 'version:   %s\n' "$version"
    printf 'date:      %s (commit timestamp, UTC)\n' "$date"
    check_toolchain "$repo_root"
    printf '\n'

    table_header
    mismatches=0
    for target in $targets; do
        build_binary "$tmpdir/a" "$target" "$version" "$commit" "$date"
        build_binary "$tmpdir/b" "$target" "$version" "$commit" "$date"
        hash_a=$(sha256_of "$tmpdir/a/$(binary_name "$target")")
        hash_b=$(sha256_of "$tmpdir/b/$(binary_name "$target")")
        if [ "$hash_a" = "$hash_b" ]; then
            result=identical
        else
            result=DIFFERENT
            mismatches=$((mismatches + 1))
        fi
        printf '%-13s %-64s %-64s %s\n' "$target" "$hash_a" "$hash_b" "$result"
    done

    printf '\n'
    printf 'note: GoReleaser stamps version.Date with the commit timestamp\n'
    printf '      (.CommitDate), so two builds of the same commit are identical.\n'
    printf '      The releases v0.1.0 and v0.1.1 stamped the build time instead\n'
    printf '      and cannot be reproduced bit for bit.\n'

    if [ "$mismatches" -ne 0 ]; then
        note "$mismatches of the targets are not reproducible"
        return "$EX_FAIL"
    fi
    note 'every target is reproducible'
    return "$EX_OK"
}

# check_archive ARCHIVE compares a released archive with a rebuild of its tag.
check_archive() {
    archive=$1
    [ -f "$archive" ] || fail "the archive is not found: $archive"
    archive_name=$(basename "$archive")
    archive_stem=${archive_name%.tar.gz}
    archive_stem=${archive_stem%.zip}
    case $archive_stem in
    varco_*) archive_stem=${archive_stem#varco_} ;;
    *) fail "the archive name is not varco_VERSION_os_arch: $archive_name" ;;
    esac
    version=${archive_stem%%_*}
    archive_target=${archive_stem#*_}
    target_os=${archive_target%%_*}
    target_arch=${archive_target#*_}
    printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' ||
        fail "the version in the archive name is not MAJOR.MINOR.PATCH: $version"

    git_use_dir "$repo_root" || fail "git does not work in $repo_root"
    tag=v$version
    if ! git rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1; then
        note "the tag $tag is not in the local clone, fetching the tags"
        git fetch --tags --quiet || fail 'cannot fetch the tags of the repository'
        git rev-parse -q --verify "refs/tags/$tag" >/dev/null 2>&1 ||
            fail "the tag $tag does not exist in the repository"
    fi

    worktree=$tmpdir/worktree
    git -c core.autocrlf=false worktree add --detach "$worktree" "$tag" >/dev/null 2>&1 ||
        fail "cannot create a git worktree for $tag"
    build_dir=$worktree
    git_use_dir "$worktree" || fail "git does not work in the worktree $worktree"

    read_release_config "$worktree"
    commit=$(git rev-parse "$tag^{commit}")
    date=$(commit_date "$tag")
    build_version=$version

    printf 'archive:   %s\n' "$archive_name"
    printf 'tag:       %s\n' "$tag"
    printf 'commit:    %s\n' "$commit"
    printf 'version:   %s\n' "$build_version"
    printf 'date:      %s (commit timestamp, UTC)\n' "$date"
    check_toolchain "$worktree"
    printf '\n'

    extract_dir=$tmpdir/extract
    mkdir -p "$extract_dir"
    case $archive_name in
    *.tar.gz|*.tgz) tar -xzf "$archive" -C "$extract_dir" || fail "cannot extract $archive" ;;
    *.zip)
        if command -v unzip >/dev/null 2>&1; then
            unzip -o -q "$archive" -d "$extract_dir" || fail "cannot extract $archive"
        else
            tar -xf "$archive" -C "$extract_dir" || fail "cannot extract $archive"
        fi
        ;;
    *) fail "the archive is neither a tar.gz nor a zip: $archive_name" ;;
    esac

    release_binary_path=$(find "$extract_dir" -type f -name "$(binary_name "$target_os/$target_arch")" | head -1)
    [ -n "$release_binary_path" ] || fail "the archive does not contain $(binary_name "$target_os/$target_arch")"

    build_binary "$tmpdir/rebuild" "$target_os/$target_arch" "$build_version" "$commit" "$date"
    rebuild_hash=$(sha256_of "$tmpdir/rebuild/$(binary_name "$target_os/$target_arch")")

    # The go command does not stamp the version control information of the
    # build in a linked git worktree, and the release binaries do carry it.
    # A build of a clone of the tag is used for the comparison in that case.
    if ! has_vcs_info "$tmpdir/rebuild/$(binary_name "$target_os/$target_arch")"; then
        clone=$tmpdir/clone
        git clone --quiet --no-checkout "$repo_root" "$clone" >/dev/null 2>&1 ||
            fail 'cannot clone the repository for the comparison'
        # The clone must hold the committed files byte for byte: the go command
        # hashes the content and the modification time of the source files into
        # the build ID of the binary.
        git -C "$clone" config core.autocrlf false >/dev/null 2>&1 ||
            fail 'cannot configure the clone for the comparison'
        git -c core.autocrlf=false -C "$clone" checkout --quiet --detach "$tag" >/dev/null 2>&1 ||
            fail "cannot check out $tag in the clone"
        build_dir=$clone
        git_use_dir "$clone" || fail "git does not work in the clone $clone"
        build_binary "$tmpdir/rebuild-clone" "$target_os/$target_arch" "$build_version" "$commit" "$date"
        rebuild_hash=$(sha256_of "$tmpdir/rebuild-clone/$(binary_name "$target_os/$target_arch")")
        printf 'note: the go command does not stamp the version control information in\n'
        printf '      a linked git worktree, the comparison uses a clone of %s\n' "$tag"
    fi

    release_hash=$(sha256_of "$release_binary_path")

    table_header
    printf '%-13s %-64s %-64s %s\n' "$target_os/$target_arch" "$release_hash" "$rebuild_hash" \
        "$( [ "$release_hash" = "$rebuild_hash" ] && printf identical || printf DIFFERENT )"

    printf '\n'
    if [ "$release_hash" = "$rebuild_hash" ]; then
        note 'the released binary is byte for byte the rebuilt binary'
        return "$EX_OK"
    fi

    note 'the released binary and the rebuilt binary differ'
    printf 'reason:   the Go toolchain of this machine is %s, the release used the\n' "$(go env GOVERSION)"
    printf '          %s of the release workflow; a different toolchain changes the binary.\n' \
        "$(grep -E 'go-version(-file)?:' "$worktree/.github/workflows/release.yml" 2>/dev/null | head -1 | sed 's/^[ \t]*//' || echo unknown)"
    printf 'reason:   GoReleaser stamps version.Date with the commit timestamp,\n'
    printf '          the releases v0.1.0 and v0.1.1 stamped the build time instead.\n'

    release_date=$(detect_built_date "$release_binary_path" "$target_os/$target_arch")
    if [ -z "$release_date" ]; then
        printf 'check:    the released binary does not run on this host, its build date is unknown\n'
    elif [ "$release_date" = "$date" ]; then
        printf 'check:    the released binary was built with the commit timestamp %s\n' "$release_date"
    else
        printf 'check:    the released binary was built at %s, rebuilding with that date\n' "$release_date"
        build_binary "$tmpdir/rebuild-dated" "$target_os/$target_arch" "$build_version" "$commit" "$release_date"
        dated_hash=$(sha256_of "$tmpdir/rebuild-dated/$(binary_name "$target_os/$target_arch")")
        if [ "$dated_hash" = "$release_hash" ]; then
            note 'the binaries are identical once version.Date holds the build time of the release'
        else
            note 'the binaries still differ with the build time of the release: the toolchain or another build input differs'
        fi
    fi
    return "$EX_FAIL"
}

# ------------------------------------------------------------------- main --

archive=
while [ "$#" -gt 0 ]; do
    case $1 in
    -h|--help)
        help
        exit "$EX_OK"
        ;;
    --)
        shift
        break
        ;;
    -*)
        printf '%s: unknown option: %s\n' "$prog" "$1" >&2
        usage
        ;;
    *)
        [ -z "$archive" ] || usage
        archive=$1
        shift
        ;;
    esac
done
[ "$#" -eq 0 ] || usage

command -v go >/dev/null 2>&1 || fail 'go is not installed'
command -v git >/dev/null 2>&1 || fail 'git is not installed'

tmpdir=$(mktemp -d)

status=0
if [ -n "$archive" ]; then
    check_archive "$archive" || status=$?
else
    check_local || status=$?
fi
exit "$status"
