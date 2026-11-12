# enproxy inbound authentication refactor

## Files

- `config.go`: new `inbounds/outbounds/accounts/route` configuration model.
- `account.go`: global downstream account manager; one shared limiter per account.
- `route.go`: inbound-tag to outbound-tag routing.
- `auth.go`: authentication request/response framing and AES-GCM selection.
- `proxy.go`: authentication-before-routing/dialing, then port/account limiting and traffic accounting.
- `proxy_manager.go`: configuration validation and multi-inbound startup.
- `relay.go`: inbound/outbound codec relay.

`codec.go`, `encrypt.go`, `ratelimit.go`, `traffic.go`, `web.go`, and `webui.go` are not modified.

## Authentication wire format

Both modes use a 4-byte big-endian frame length. When `inbound.crypto.enabled=true`, the frame body is the existing `CryptoCodec` AES-GCM ciphertext (nonce + ciphertext + tag). When false, the frame body is the plaintext payload.

Request payload:

| Field | Size |
|---|---:|
| Magic `ENPA` | 4 bytes |
| Version `1` | 1 byte |
| Type `1` (request) | 1 byte |
| Username length | 2 bytes, big endian |
| Password length | 2 bytes, big endian |
| Username | variable |
| Password | variable |

Response payload: `ENPA` (4 bytes), version (1), type `2` (1), status (1; `0` success, `1` failure).

The authentication frame is consumed by the inbound proxy and is never forwarded to the outbound. Authentication is performed before rate-limit and traffic wrappers are attached, so auth bytes are excluded from business counters and limiters.

## Notes

- AES keys must be 16, 24, or 32 bytes. The existing `encrypt.go` uses a fresh random GCM nonce for every encrypted frame and includes it in the ciphertext.
- If crypto is disabled, username/password are sent in plaintext by design. Use crypto when credentials must be protected in transit.
- `outbounds[].account` is represented in configuration but is not sent automatically: plain `protocol: tcp` has no universal username/password login handshake. Implement a protocol-specific outbound handler before relying on it.
- The existing `PlainCodec` remains a stream codec for business traffic. Authentication uses its own length-prefixed plaintext framing to avoid TCP read-boundary assumptions.
- Update the program entry point to call `NewProxyManager(config, trafficManager)` (the constructor now accepts the complete `Config`, not `[]ProxyConfig`). Start the traffic sampler as before, e.g. `go trafficManager.Start()`, then call `manager.Start()`.
