#!/usr/bin/env python3

import argparse
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "sing-box"
PATCH_DIR = ROOT / "patches"


def run(*args: str, cwd: Path = ROOT, capture: bool = False) -> str:
    result = subprocess.run(
        args,
        cwd=cwd,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
    )
    return result.stdout.strip() if capture else ""


def read_upstream_commit() -> str:
    values = {}
    for line in (PATCH_DIR / "upstream.txt").read_text(encoding="utf-8").splitlines():
        key, separator, value = line.partition("=")
        if separator:
            values[key.strip()] = value.strip()
    commit = values.get("commit", "")
    if not commit:
        raise RuntimeError("patches/upstream.txt does not define commit")
    return commit


def read_series() -> list[Path]:
    patches = []
    for line in (PATCH_DIR / "series").read_text(encoding="utf-8").splitlines():
        name = line.strip()
        if not name or name.startswith("#"):
            continue
        patch = PATCH_DIR / name
        if not patch.is_file():
            raise RuntimeError(f"missing patch: {patch}")
        patches.append(patch)
    if not patches:
        raise RuntimeError("patches/series is empty")
    return patches


def prepare(output: Path) -> None:
    if not SOURCE.is_dir():
        raise RuntimeError("sing-box submodule is not initialized")
    if output.exists():
        raise RuntimeError(f"output already exists: {output}")

    commit = read_upstream_commit()
    source_head = run("git", "rev-parse", "HEAD", cwd=SOURCE, capture=True)
    if source_head != commit:
        raise RuntimeError(
            f"sing-box submodule is at {source_head}, expected {commit}"
        )
    source_status = run("git", "status", "--porcelain", cwd=SOURCE, capture=True)
    if source_status:
        raise RuntimeError("sing-box submodule has local changes")

    output.parent.mkdir(parents=True, exist_ok=True)
    run("git", "worktree", "add", "--detach", str(output), commit, cwd=SOURCE)
    try:
        for patch in read_series():
            run("git", "apply", "--index", str(patch), cwd=output)
    except BaseException:
        subprocess.run(
            ["git", "worktree", "remove", "--force", str(output)],
            cwd=SOURCE,
            check=False,
        )
        raise

    print(output)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Create a disposable official sing-box worktree and apply Ackwrap patches."
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=ROOT / ".work" / "sing-box",
        help="new worktree path (default: .work/sing-box)",
    )
    args = parser.parse_args()
    output = args.output
    if not output.is_absolute():
        output = (ROOT / output).resolve()
    prepare(output)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"prepare_core: {error}", file=sys.stderr)
        sys.exit(1)
