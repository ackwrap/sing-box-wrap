# Repository-specific agent notes

## Repository boundary

- This repository is a wrapper, not a sing-box source fork.
- `sing-box/` is the unmodified official SagerNet/sing-box submodule.
- Ackwrap production changes are the ordered patches listed in `patches/series`.
- The pinned official commit is recorded both by the gitlink and in
  `patches/upstream.txt`; they must always match.
- Do not edit files inside `sing-box/` directly.
- The initial patch stack excludes Ackwrap-specific test files. Do not add test
  patches until the production stack migration is complete and explicitly
  approved.

## Development flow

- `main` is protected. Make changes on a non-main feature branch.
- Run `python scripts/prepare_core.py` to create `.work/sing-box` with all
  patches applied.
- Develop in a disposable patched worktree, then export the production diff to
  the appropriate logical patch. Never commit generated `.work/` content.
- Keep patches separated by feature and ordered by `patches/series`; do not
  replace the stack with one aggregate diff.
- An upstream update changes only the `sing-box` gitlink,
  `patches/upstream.txt`, and patches that genuinely need rebasing.
- Upstream updates must be proposed on a feature branch and reviewed; do not
  force-update protected branches.

## Verification

- Patch preparation must succeed from a clean clone with initialized
  submodules.
- `go mod tidy` in the prepared worktree must produce no diff.
- Run `make build` and `go build ./...` in the prepared worktree.
- Run focused tests when test patches are introduced later.
- Do not modify, remove, or weaken upstream tests to make verification pass.

## Operational safety

- Do not commit or push generated core source or build artifacts.
- Do not publish releases, update tags, or change release credentials without
  explicit approval.
- Do not delete legacy branches until this wrapper structure has been merged,
  cloned from scratch, and independently verified.
