# Repository-specific agent notes

## Repository boundaries

- The root module is `github.com/sagernet/sing-box` (Go `1.24.7`); the CLI entry point is `./cmd/sing-box`.
- Preserve `go.mod`'s replacement of `github.com/sagernet/sing-vmess` with `github.com/ackwrap/sing-vmess`; it is a fork-specific change, not dependency drift.
- Do not modify `go.sum` unless the requested change actually requires dependency resolution. Reuse existing functions and framework components instead of duplicating equivalent logic.
- `test/` is a separate Go module that replaces `github.com/sagernet/sing-box` with `../`. Root `go test ./...` does not include it.
- `clients/android/` and `clients/apple/` are embedded client projects. However, many root `Makefile` release targets still operate on sibling checkouts `../sing-box-for-android` and `../sing-box-for-apple`; use the client-local Gradle wrapper or Apple `Makefile` for these embedded trees.

## Protocol development

- Add new protocols in a dedicated directory where practical. Reuse the existing protocol framework and follow comparable in-tree implementations rather than creating a parallel architecture.
- Prefer implementing protocol behavior in this repository based on the established framework instead of directly importing another protocol implementation. Confirm with the user before adding such an external implementation or dependency.

## Branch flow

- `sync` mirrors the official upstream branch, `devel` is the development branch, and `main` is the primary branch. Normal promotion order is `sync` -> `devel` -> `main`; do not bypass it unless explicitly requested.
- `devel` is the merge boundary between upstream synchronization and Ackwrap development. Before pull, merge, rebase, checkout, or a parent-repository submodule update, run `git status --short` here and stop if the worktree is dirty.
- Never run reset, clean, `git submodule update --force`, or another command that moves this worktree while uncommitted protocol files exist. Such an update previously moved the worktree from the parent-recorded commit to `origin/devel` and removed an uncommitted SSR implementation.
- For core features, verify, commit, and push this repository first. Only then update and commit the parent repository's submodule pointer.
- After any branch synchronization, verify `git rev-parse HEAD`, `git status --short`, and the expected feature paths before continuing.

## Go verification

- `make build` and `make race` set `GOTOOLCHAIN=local` and build `./cmd/sing-box` with tags from `release/DEFAULT_BUILD_TAGS_OTHERS` unless `TAGS` is overridden.
- Focus a root test with `go test ./path/to/package -run '^TestName$'`. For CI parity, also pass tags from `release/DEFAULT_BUILD_TAGS_OTHERS` and ldflags from `release/LDFLAGS`; Unix CI additionally runs tests through `sudo` via `-exec sudo`.
- Focus an integration test from `test/` with `go test -run '^TestName$' .` (plus any required `-tags`). These tests create Docker containers with host networking, so they require a reachable Docker daemon and may bind fixed host ports.
- `make test` first tests the root module, then enters `test/`, runs `go mod tidy`, and runs its suite. It can modify `test/go.mod` or `test/go.sum`; `make test_stdio` adds `force_stdio` to the integration tags.
- `make lint` runs golangci-lint for Linux, Android, Windows, and Darwin. `.golangci.yml` supplies the repository build tags and uses `gci` plus `gofumpt`; `make fmt` can rewrite files.

## Generated and mobile code

- Do not hand-edit `*.pb.go`. `make proto` regenerates every repository `.proto`, normalizes generated headers, and formats the repository; it requires `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`, and `gofumpt`.
- `make lib_android` requires the repository gomobile tools (`make lib_install`), OpenJDK 17, an Android SDK, and NDK `28.0.13004108` (another installed NDK is accepted with a reproducibility warning). It emits `libbox.aar` and `libbox-legacy.aar` at the repository root; copy them to `clients/android/app/libs/` before Gradle builds.
- Run Android commands from `clients/android/` with `./gradlew`; formatting is `./gradlew spotlessApply`, checking includes `./gradlew spotlessCheck detekt`, and app variants are `other`, `otherLegacy`, and `play`.
- `make lib_apple` requires gomobile and the Apple/Xcode toolchain. It creates `Libbox.xcframework` at the repository root, but moves it to `../sing-box-for-apple` when that sibling exists; place the framework in `clients/apple/` before building the embedded client. The Apple client uses `make fmt` (`swiftformat`) and `make lint` (`swiftlint`) from that directory.

## Dangerous commands

- Never run root `make update`: it fetches, hard-resets to `FETCH_HEAD`, and runs `git clean -fdx`.
- Treat release, upload, publish, notarize, and version-update targets as operational commands; several use credentials, mutate versions/tags, or publish artifacts.
