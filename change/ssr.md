# ShadowsocksR Outbound Maintenance Record

## Scope

Ackwrap's sing-box fork restores ShadowsocksR as an outbound only. The removed
ShadowsocksR inbound remains a compatibility stub and still returns the
upstream removal error.

## Why This Exists

Upstream sing-box removed ShadowsocksR in 1.6.0, but retained the
`shadowsocksr` type constant and option schema for migration errors. Ackwrap
maintains its own core and needs to consume existing SSR subscriptions without
silently discarding those nodes.

## Implementation

- Registry type: `shadowsocksr`
- Option schema: existing `option.ShadowsocksROutboundOptions`
- Protocol/obfs baseline: upstream sing-box 1.5.5 `transport/clashssr`, copied into this fork and adapted to fork-internal pool, tools, KDF, and cipher packages
- Cipher implementation: fork-maintained `transport/clashssr/shadowstream`, using the Go standard library and the already-present `golang.org/x/crypto/chacha20`
- Core adapter: `protocol/shadowsocksr/outbound.go`
- Supported networks: TCP and native SSR UDP packet transport
- Construction validates the cipher, protocol, and obfs names before the core
  starts
- Dialing uses the request context through the current sing-box dialer, so
  detours, interface binding, domain resolution, and cancellation remain under
  sing-box control
- No Mihomo, Clash, or third-party SSR runtime package is imported

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

The fork supports AES-128/192/256 CFB, CTR and OFB, RC4-MD5, RC4-MD5-6,
ChaCha20-IETF, and `none`/`dummy`. Other classic or AEAD methods are rejected
during outbound construction instead of being silently downgraded.

## Ackwrap Mapping

Ackwrap keeps `ssr` as the database and UI protocol name. Configuration
generation converts it to the core type `shadowsocksr` and maps:

- `cipher` to `method`
- `obfs-param` to `obfs_param`
- `protocol-param` to `protocol_param`
- Clash `udp: false` to `network: tcp`; true or omitted keeps TCP+UDP

Both SSR URI and Clash YAML imports use this mapping.

## Known Limitations

- SSR is a legacy protocol. New cipher, protocol, or obfs variants must be
  added deliberately with interoperability tests.
- Configuration validation proves schema and constructor support; a real
  server is still required for protocol interoperability testing.

## Verification

Run from `sing-box-wrap`:

```bash
go test ./protocol/shadowsocksr ./include
go build ./cmd/sing-box
```

Then validate a redacted configuration containing a `shadowsocksr` outbound:

```bash
sing-box check -c ssr-test.json
```
