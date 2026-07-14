# Core Build Artifact Matrix

## Supported release targets

The `Build Core Service` workflow intentionally publishes only the targets
used by Ackwrap:

- Windows: amd64 and arm64
- Linux: amd64 and arm64, each in purego, glibc, and musl variants
- macOS: arm64
- OpenWrt: x86_64 and aarch64 variants (`aarch64_generic`, Cortex-A53,
  Cortex-A72, and Cortex-A76), in both IPK and APK formats

## Excluded targets

The workflow does not build or publish 386, 32-bit ARM, MIPS, MIPS64,
RISC-V, LoongArch, S390x, PPC64LE, Windows 7 legacy, macOS amd64, or macOS
10.13 legacy artifacts.

When changing the matrix, update both the build jobs and this record. The
release upload job downloads all workflow artifacts automatically, so excluded
architectures must be removed from the producing matrices rather than filtered
only during release upload.
