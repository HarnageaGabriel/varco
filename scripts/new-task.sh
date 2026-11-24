#!/bin/sh
set -eu

usage() { echo "usage: $0 [--dry-run] ISSUE [SLUG]" >&2; exit 2; }
dry_run=false
if [ "${1:-}" = "--dry-run" ]; then dry_run=true; shift; fi
[ "$#" -ge 1 ] && [ "$#" -le 2 ] || usage
issue=$1
title=$(gh issue view "$issue" --json title --jq .title)
slug=${2:-$(printf '%s' "$title" | tr -d '[:cntrl:]' | tr '[:upper:]' '[:lower:]' | sed 's/[^a-z0-9][^a-z0-9]*/-/g; s/^-//; s/-$//' | cut -c 1-40 | sed 's/-$//')}
[ -n "$slug" ] || { echo "cannot derive a slug" >&2; exit 1; }
branch="task/$issue-$slug"
path=".worktrees/$issue-$slug"
if git show-ref --verify --quiet "refs/heads/$branch" || git show-ref --verify --quiet "refs/remotes/origin/$branch"; then
  echo "branch exists: $branch" >&2; exit 1
fi
[ ! -e "$path" ] || { echo "directory exists: $path" >&2; exit 1; }
if $dry_run; then
  printf '%s\n' "git fetch origin" "git worktree add -b $branch $path origin/main" "path: $path"
  exit 0
fi
git fetch origin
git worktree add -b "$branch" "$path" origin/main
echo "$path"
