#!/bin/sh
set -eu

dry_run=false
if [ "${1:-}" = "--dry-run" ]; then dry_run=true; shift; fi
[ "$#" -gt 0 ] || { echo "usage: $0 [--dry-run] PR..." >&2; exit 2; }
poll=${MERGE_QUEUE_POLL:-30}; timeout=${MERGE_QUEUE_TIMEOUT:-2700}; parked=false
prs="$*"
state() { gh pr view "$1" --json state,mergeStateStatus,headRefName --jq '.state+" "+.mergeStateStatus+" "+.headRefName'; }
checks() {
  # gh exits 8 (pending) / 1 (failing) even with --json; keep its output, empty means pending.
  out=$(gh pr checks "$1" --json bucket --jq 'if length == 0 then "pending" elif any(.[]; .bucket == "pending") then "pending" elif any(.[]; .bucket == "fail" or .bucket == "cancel") then "failed" else "green" end' || true)
  if [ -z "$out" ]; then out=pending; fi
  printf '%s\n' "$out"
}
parse() {
  # Intentional word splitting: info is "STATE MERGE_STATE BRANCH".
  # shellcheck disable=SC2086
  set -- $1
  pr_state=$1; merge_state=$2; branch=$3
}
# Intentional word splitting: prs is the saved space-separated PR list.
# shellcheck disable=SC2086
for pr in $prs; do
  info=$(state "$pr"); parse "$info"
  [ "$pr_state" = OPEN ] || { echo "$pr: skip (not open)"; continue; }
  if $dry_run; then echo "$pr: $merge_state $(checks "$pr") ($branch)"; continue; fi
  waited=0; result=; behind_timeout=false
  while :; do
    if [ "$merge_state" = BEHIND ]; then
      echo "$pr: updating branch"
      old_head=$(gh pr view "$pr" --json headRefOid --jq .headRefOid)
      gh pr update-branch "$pr" || { echo "$pr: parked (branch update failed)"; parked=true; behind_timeout=true; break; }
      head=$old_head
      while [ "$head" = "$old_head" ]; do
        [ "$waited" -lt "$timeout" ] || { echo "$pr: parked (branch update not visible)"; parked=true; behind_timeout=true; break; }
        sleep "$poll"; waited=$((waited + poll))
        head=$(gh pr view "$pr" --json headRefOid --jq .headRefOid)
      done
      $behind_timeout && break
      info=$(state "$pr"); parse "$info"
      continue
    fi
    result=$(checks "$pr")
    [ "$result" = pending ] || break
    [ "$waited" -lt "$timeout" ] || { echo "$pr: parked (checks still pending)"; parked=true; break; }
    sleep "$poll"; waited=$((waited + poll)); info=$(state "$pr"); parse "$info"
  done
  $behind_timeout && continue
  if [ "$result" != green ] || [ "$merge_state" != CLEAN ]; then
    if [ "$result" = failed ]; then echo "$pr: parked (check failed)"; else echo "$pr: parked (state $merge_state, checks $result)"; fi
    parked=true
    continue
  fi
  gh pr merge "$pr" --squash || { echo "$pr: parked (merge failed)"; parked=true; continue; }
  echo "$pr: merged"
  after=false
  # Intentional word splitting: prs is the saved space-separated PR list.
  # shellcheck disable=SC2086
  for remaining in $prs; do
    if $after; then gh pr update-branch "$remaining" >/dev/null 2>&1 || true; fi
    [ "$remaining" = "$pr" ] && after=true
  done
done
if $parked; then exit 1; fi
exit 0