# ShadowsocksR Outbound Maintenance Record

## Scope

Ackwrap's sing-box fork restores ShadowsocksR as an outbound only. The removed ShadowsocksR inbound remains a compatibility stub.

## Implementation

- Registry type: `shadowsocksr`
- Protocol/obfs baseline: upstream sing-box 1.5.5 `transport/clashssr`, copied into this fork and adapted to fork-internal pool, tools, KDF, and cipher packages
- Cipher implementation: fork-maintained `transport/clashssr/shadowstream`, using the Go standard library and the existing `golang.org/x/crypto/chacha20`
- Supported networks: TCP and native SSR UDP packet transport
- No Mihomo, Clash, or third-party SSR runtime package is imported
- HTTP and TLS obfs handshake responses support normal TCP segmentation
- TLS handshake state and auth-chain UDP PRNG state are serialized; malformed UDP padding is rejected before slicing

## Supported Protocols

- `origin`
- `auth_sha1_v4`
- `auth_aes128_md5`
- `auth_aes128_sha1`
- `auth_chain_a`
- `auth_chain_b`

## Supported Obfuscation

- `plain`
- `http_simple`
- `http_post`
- `random_head`
- `tls1.2_ticket_auth`
- `tls1.2_ticket_fastauth`

## Supported Ciphers

AES-128/192/256 CFB, CTR and OFB, RC4-MD5, RC4-MD5-6, ChaCha20-IETF, and `none`/`dummy`. Unsupported classic or AEAD methods are rejected during outbound construction.

## Ackwrap Mapping

Ackwrap keeps `ssr` as the database/UI name and maps it to `shadowsocksr`; `cipher` becomes `method`, hyphenated protocol/obfs parameters become underscore fields, and Clash `udp: false` becomes `network: tcp`.

## Branch Safety

`devel` is the merge boundary between upstream synchronization and Ackwrap changes. Commit and push core changes before the parent repository updates its submodule pointer. Never move a dirty submodule worktree during merge, pull, rebase, checkout, or submodule update.

## Verification

```bash
go test ./transport/clashssr/... ./protocol/shadowsocksr ./include
go build ./cmd/sing-box
```
