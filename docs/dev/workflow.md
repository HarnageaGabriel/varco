# Microtask workflow

This is the project process for small tasks that can move independently. It
works for human contributors and AI agents.

## 1. Backlog

GitHub issues are the backlog. One issue is one microtask, small enough for one
pull request: XS is under an hour, S is a few hours, and M is a day or two.
Anything larger is split first.

Use area/* for what the task concerns, size/* for effort, and status/ready,
status/in-progress, or status/blocked for state. agent-ready means the issue is
specified well enough to start without asking anyone. needs-owner means it
needs a repository-owner decision or external action. maintainer marks an
internal task. Contributor-facing tasks keep good first issue and help wanted
where appropriate.

A milestone is the next release, such as 0.1.3, and plays the role of the
sprint.

## 2. Definition of Ready

An issue may carry status/ready only when it has all of these:

- An objective in one sentence.
- A scope with TOUCH ONLY and DO NOT TOUCH lists.
- Acceptance criteria that can be checked.
- The exact verify commands.
- A Parallel safety line naming its files and the other open issues touching
  those files.

## 3. Parallel rules

One issue equals one branch, one worktree, and one pull request. Two issues run
at the same time only when their TOUCH ONLY sets are disjoint.

Hot files are never touched by microtasks unless the issue says so:
CHANGELOG.md is edited only by the release preparation PR; each PR puts its
changelog line in the PR description under a Changelog: heading.
README.md gets batch edits in one docs task. go.mod and go.sum change only for
dependencies.

Generated files in docs/diagnostics and docs/reference are regenerated, never
hand-edited.

Take work in waves. Pick the ready issues in the milestone whose file sets are
disjoint, start them together, merge them with the queue, then start the next
wave.

## Labels at a glance

| Label | Meaning |
| --- | --- |
| area/* | What part of the project the task concerns |
| size/XS, size/S, size/M | Expected effort |
| status/ready | Meets Definition of Ready |
| status/in-progress | Someone is actively working |
| status/blocked | Work needs an unresolved dependency |
| agent-ready | Clear enough to start without questions |
| needs-owner | Owner decision or external action is needed |
| maintainer | Internal maintainer work |
| good first issue | Suitable for a new contributor |
| help wanted | Maintainer welcomes contributor help |

## 4. Branches and commits

Run scripts/new-task.sh ISSUE [SLUG]. It creates task/<issue-number>-<slug>
and a matching .worktrees/<issue>-<slug> from origin/main. It does not change
the current checkout.

Commits are signed off with git commit -s. Use a conventional prefix: feat,
fix, docs, test, or chore. Do not add attribution lines.

## 5. Pull requests

The description starts with Closes #N, or Refs #N when it only advances the
issue. Then include a summary, a Changelog: line (or Changelog: none), the
verify commands and their results, and risks.

Request an adversarial review for probe, TLS, and redaction changes, as
required by CONVENTIONS.md.

## 6. Definition of Done

Acceptance criteria pass. make fmt test lint docs-check passes. CI is green.
The review rules in CONVENTIONS.md were applied. Docs and goldens are updated.
No dependency was added without a written reason. The issue is closed.

## 7. Closing issues

Normally, a squash merge of a PR whose description says Closes #N closes the
issue. If the task ends with an action instead of a change, a maintainer closes
it by hand with a comment carrying evidence: a link, command output, or the
decision. An unnecessary task is closed with a comment explaining why.

Anyone with triage rights may close an issue. Do not close without evidence.

## 8. Merging

Run scripts/merge-queue.sh PR... for the queue. It merges green PRs one at a
time with squash. After every merge it updates each remaining branch from
main, because main requires linear history and an up-to-date branch.

It never merges a PR whose checks are not all successful. Use --dry-run to
print the plan and current state without changing anything.

## 9. Releases

Release preparation is its own task. Build the changelog section from the PR
Changelog: lines, then follow the checklist in docs/release-process.md. Tags
and publishing are owner actions.

## 10. Retrospective

At the end of a wave, write one paragraph as a comment on the milestone or in
the release PR: what was merged, what was parked and why, and what to change
in the process.

## Worked example

Issue #123 says: update one guide, lists only that guide under TOUCH ONLY, and
gives exact checks. It receives status/ready, agent-ready, area/docs, and
size/XS. The contributor runs scripts/new-task.sh 123 guide-links, edits the
worktree, runs the listed checks, and opens a PR beginning Closes #123 with
Changelog: none. The queue sees green checks, updates any remaining branches,
squash-merges the PR, and GitHub closes #123.

## FAQ

What if two tasks need the same file? Sequence them or merge them into one
task.

What if CI is red on an unrelated flake? Re-run it once, then open a task for
the flake.

What if the task grows? Stop, split it, and open new issues.

Every issue should name its owner when an owner action is required. A blocked
issue should not be started just to discover the same blocker again.

When a task is handed between people or agents, leave the branch name, current
checks, and the next concrete action in the issue. This keeps the work
recoverable without a private handoff.

Keep the scope lists authoritative. If a new file becomes necessary, stop and
update the issue before editing it. Re-check parallel safety before opening
the pull request.

A wave ends when its ready set is merged or explicitly parked. Parked work
returns to a later wave only after its status and acceptance criteria are
current.
