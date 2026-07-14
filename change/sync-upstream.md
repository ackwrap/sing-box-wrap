# Upstream Sync Workflow

## Branch flow

The scheduled workflow mirrors upstream `testing` into `sync`, then attempts to
merge `sync` into `devel`. A merge conflict creates a pull request instead of
forcing changes into `devel`.

## Workflow file boundary

GitHub's built-in Actions token can update normal repository contents but is
not allowed to introduce upstream changes under `.github/workflows`. Before
pushing `sync`, the job restores that directory from the previous
`origin/sync` tree and creates a preservation commit when necessary.

The resulting sync commit is verified to have no workflow-tree difference from
the previous remote sync ref. Runtime source, options, tests, modules, and
non-workflow metadata still mirror upstream normally.

This design avoids a long-lived personal token and keeps Ackwrap-owned workflow
definitions outside automatic upstream replacement.
