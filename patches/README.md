# Ackwrap sing-box patches

The `sing-box/` submodule is an unmodified checkout of the official
SagerNet/sing-box repository. Ackwrap behavior is maintained by the ordered
patch list in `series`.

Patches must apply in order to the commit recorded in `upstream.txt`. Do not
edit the `sing-box/` submodule in place. Use the repository preparation script
to create a disposable patched worktree for development, builds, and tests.

The initial patch stack contains production code only. Ackwrap-specific test
files remain in the legacy fork history and will be migrated separately after
the production stack is stable. Tests already provided by upstream remain in
the official submodule.

Patch groups:

1. Runtime routing, node exposure, and the runtime API.
2. Outbound exit-IP reporting through the Clash API.
3. Full certificate SHA-256 pinning across supported TLS engines.
4. ShadowsocksR outbound support.
5. VLESS encryption and the Ackwrap sing-vmess dependency.
6. Atomic OOM draft throttling on Darwin.
