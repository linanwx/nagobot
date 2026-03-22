---
name: push
description: Use when user says "推送", "打tag", "推一下", "部署", "update", or wants to release current changes to production
---
# Push & Deploy

Tag, push, wait for CI, update local binary.

## Steps

1. **Uncommitted changes?** Run `git status`. If dirty, ask user whether to commit first.
2. **Smoke test**: `go build ./...`. Stop if fails.
3. **Latest tag**: `git tag --list --sort=-v:refname | head -1`. Increment patch.
4. **Push**: `git tag <new> && git push origin main <new>`
5. **Wait CI**: `gh run list --limit 2 --json databaseId,status,headBranch` → find tag run → `gh run watch <id> --exit-status`. Stop if fails.
6. **Update**: `nagobot update --pre`
7. **Confirm**: Report version + restart status.

## Rules

- Never push code that doesn't compile.
- Never guess the tag — always check latest first.
- If CI fails, report and stop. Do not retry.
