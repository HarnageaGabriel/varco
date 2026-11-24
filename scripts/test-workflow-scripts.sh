#!/bin/sh
set -eu
root=$(CDPATH='' cd -- "$(dirname "$0")/.." && pwd)
tmp=${TMPDIR:-/tmp}/workflow-test-$$
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
mkdir -p "$tmp/bin"
log=$tmp/log
cat >"$tmp/bin/gh" <<'EOF'
#!/bin/sh
echo "gh $*" >> "$TEST_LOG"
case "$*" in
  *"issue view 7"*) echo 'A Useful Task' ;;
  *"issue view 8"*) echo 'Fix: the bug!!' ;;
  *"issue view 9"*) echo 'A very long task title that goes on and on and on' ;;
  *"issue view 10"*) printf 'Multi\nline\ttitle' ;;
  *"pr view 3"*)
    case "$*" in
      *headRefOid*) [ -f "$TEST_TMP/updated3" ] && echo head3b || echo head3a ;;
      *) [ -f "$TEST_TMP/updated3" ] && echo 'OPEN CLEAN branch3' || echo 'OPEN BEHIND branch3' ;;
    esac ;;
  *"pr view 1"*) echo 'OPEN CLEAN branch1' ;;
  *"pr view 2"*) echo 'OPEN CLEAN branch2' ;;
  *"pr view 4"*) echo 'OPEN CLEAN branch4' ;;
  *"pr checks 1"*|*"pr checks 3"*) echo green ;;
  *"pr checks 2"*) echo failed; exit 1 ;;
  *"pr checks 4"*) echo pending; exit 8 ;;
  *"pr update-branch 3"*)
    if [ "${TEST_UPDATE_FAIL:-0}" = 1 ]; then echo 'update-branch: merge conflict' >&2; exit 1; fi
    [ "${TEST_UPDATE_VISIBLE:-0}" = 1 ] && touch "$TEST_TMP/updated3" ;;
  *"pr merge"*) echo merged ;;
esac
exit 0
EOF
cat >"$tmp/bin/git" <<'EOF'
#!/bin/sh
echo "git $*" >> "$TEST_LOG"
case "$*" in
  *"show-ref"*) [ "${TEST_BRANCH_EXISTS:-0}" = 1 ] && exit 0 || exit 1 ;;
esac
exit 0
EOF
chmod +x "$tmp/bin/gh" "$tmp/bin/git"
export PATH="$tmp/bin:$PATH" TEST_LOG="$log" TEST_TMP="$tmp" MERGE_QUEUE_POLL=0 MERGE_QUEUE_TIMEOUT=0

out=$("$root/scripts/new-task.sh" --dry-run 7)
echo "$out" | grep -q 'git worktree add -b task/7-a-useful-task '
[ "$(echo "$out" | grep '^path: ')" = 'path: .worktrees/7-a-useful-task' ]
out=$("$root/scripts/new-task.sh" --dry-run 8)
[ "$(echo "$out" | grep '^path: ')" = 'path: .worktrees/8-fix-the-bug' ]
out=$("$root/scripts/new-task.sh" --dry-run 9)
[ "$(echo "$out" | grep '^path: ')" = 'path: .worktrees/9-a-very-long-task-title-that-goes-on-and' ]
out=$("$root/scripts/new-task.sh" --dry-run 10)
[ "$(echo "$out" | grep '^path: ')" = 'path: .worktrees/10-multilinetitle' ]

TEST_BRANCH_EXISTS=1
export TEST_BRANCH_EXISTS
if "$root/scripts/new-task.sh" --dry-run 7 >/dev/null 2>&1; then echo 'FAIL: existing branch not refused' >&2; exit 1; fi
unset TEST_BRANCH_EXISTS

: > "$log"
out=$("$root/scripts/merge-queue.sh" --dry-run 1 2 3 4)
echo "$out" | grep -q '^1: CLEAN green (branch1)$'
echo "$out" | grep -q '^2: CLEAN failed (branch2)$'
echo "$out" | grep -q '^3: BEHIND green (branch3)$'
echo "$out" | grep -q '^4: CLEAN pending (branch4)$'
grep -Eq 'pr merge|pr update-branch' "$log" && exit 1

: > "$log"
set +e
"$root/scripts/merge-queue.sh" 1 2 3 4 >"$tmp/out" 2>&1
code=$?
set -e
[ "$code" -eq 1 ]
grep -q 'pr merge 1 --squash' "$log"
grep -q 'pr update-branch 2' "$log"
grep -q 'pr update-branch 3' "$log"
grep -q 'pr update-branch 4' "$log"
grep -Eq 'pr merge (2|3|4)' "$log" && exit 1
grep -q '2: parked (check failed)' "$tmp/out"
grep -q '3: parked (branch update not visible)' "$tmp/out"
grep -q '4: parked (checks still pending)' "$tmp/out"

: > "$log"
set +e
"$root/scripts/merge-queue.sh" 2 1 >/dev/null 2>&1
code=$?
set -e
[ "$code" -eq 1 ]
grep -q 'pr merge 1 --squash' "$log"
grep -q 'pr update-branch' "$log" && exit 1

: > "$log"
set +e
"$root/scripts/merge-queue.sh" 4 1 >"$tmp/out" 2>&1
code=$?
set -e
[ "$code" -eq 1 ]
grep -q '4: parked (checks still pending)' "$tmp/out"
grep -q 'pr merge 1 --squash' "$log"

: > "$log"
TEST_UPDATE_FAIL=1
export TEST_UPDATE_FAIL
set +e
"$root/scripts/merge-queue.sh" 3 1 >"$tmp/out" 2>&1
code=$?
set -e
[ "$code" -eq 1 ]
grep -q '3: parked (branch update failed)' "$tmp/out"
grep -q 'pr merge 1 --squash' "$log"
unset TEST_UPDATE_FAIL

: > "$log"
TEST_UPDATE_VISIBLE=1
export TEST_UPDATE_VISIBLE
MERGE_QUEUE_TIMEOUT=5 "$root/scripts/merge-queue.sh" 3 >"$tmp/out" 2>&1
grep -q '3: merged' "$tmp/out"
grep -q 'pr merge 3 --squash' "$log"
unset TEST_UPDATE_VISIBLE

echo 'workflow script tests passed'