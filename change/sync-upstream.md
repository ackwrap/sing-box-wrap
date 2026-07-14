# Upstream Sync Workflow

## Branch flow

The scheduled workflow mirrors upstream `testing` into `sync`, then attempts to
merge `sync` into `devel`. A merge conflict creates a pull request instead of
forcing changes into `devel`.

## Authentication

The workflow uses the repository Actions secret `ACKWRAPBUILD` for checkout,
Git pushes, and conflict pull requests. The token must belong to an account
with write access to this repository and provide:

- Contents: read and write
- Workflows: read and write
- Pull requests: read and write

The preflight step fails with a clear message when the secret is missing. Using
the built-in `GITHUB_TOKEN` is insufficient because upstream synchronization
can update files under `.github/workflows`.
