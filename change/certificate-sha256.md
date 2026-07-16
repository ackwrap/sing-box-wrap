# Certificate SHA-256 Pin Maintenance Record

## Scope

Ackwrap's sing-box fork adds an outbound TLS `certificate_sha256` option for protocols such as Hysteria2 whose shared URI pins the complete leaf certificate rather than its public key.

## Semantics

- `certificate_sha256` hashes the leaf certificate DER bytes from `rawCerts[0]` with SHA-256.
- Values are 64-character hexadecimal fingerprints. Uppercase, colon-separated, and hyphen-separated forms are accepted.
- A configured certificate pin is enforced in addition to normal CA and hostname verification. Self-signed Hysteria2 certificates require `insecure: true`, and the pin remains enforced in that mode.
- `certificate_public_key_sha256` keeps its existing SPKI public-key semantics.
- Complete-certificate and public-key pins cannot be configured together.

## TLS Engines

The Go standard TLS, uTLS, Windows Schannel, and Apple Network.framework clients enforce the complete-certificate pin after the handshake exposes the peer certificate. The Apple HTTP engine rejects this option explicitly because its URLSession path cannot apply the same verifier.

## Verification

```bash
go test ./common/tls ./common/httpclient
go build ./cmd/sing-box
```
