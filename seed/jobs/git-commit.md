---
name: git-commit
description: Stage files and commit changes to a git repository
profile: coder
params:
  - name: path
    required: true
    description: Absolute path to the git repository root
  - name: message
    required: true
    description: Commit message
  - name: files
    required: false
    default: "."
    description: Files or paths to stage (space-separated, passed to git add). Defaults to all changes.
---
Your task is to commit changes in a git repository. Do NOT push.

## Steps

1. `cd {{path}}`
2. Run `git status` to see the current state of the working tree.
3. Run `git diff` (staged and unstaged) to review what will be committed.
4. Stage files: `git add {{files}}`
5. Commit: `git commit -m "{{message}}"`
6. Run `git log -1 --oneline` to confirm the commit and capture the hash.

## Report

When done, report:
- Commit hash and message
- Files that were staged and committed
- Any warnings or issues encountered (e.g. nothing to commit, hook failures)

Do NOT push to remote.
