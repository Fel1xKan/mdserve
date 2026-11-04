# Security model

mdserve protects a folder of documents from casual access. It is not a substitute for
identity-based access control.

## What the password is for

The password keeps search engines, link scanners, and people who guess a URL away from the
content. It does not protect against a motivated attacker who can reach the service: there is
one shared secret per site, no per-user identity, and no audit trail of who read what.

If the documents are genuinely sensitive, put **Cloudflare Access** in front and authenticate
with email or SSO. mdserve then sits behind a second, stronger gate.

## Sessions

A session token looks like:

```text
v1.<expiry>.<nonce>.<mac>
```

The MAC is HMAC-SHA256 over the host, a server epoch, and the payload, keyed by a 32-byte
secret generated on first start. Changing the host invalidates the token, so a cookie issued
for one site is useless on another.

Cookies are `HttpOnly` and `SameSite=Lax`, and gain `Secure` when the public URL is HTTPS.
The absolute lifetime is 8 hours; the idle lifetime is 30 minutes and is renewed on each
successful request.

`mdserve revoke` rotates the epoch file. Every session and every outstanding share code stops
working within two seconds, without restarting the process.

## Share codes

Share codes use the same construction with an `s1.` prefix. The server remembers used nonces
for the lifetime of the code, so a code works exactly once. They expire after one hour by
default and are bound to a host.

## Login hardening

- Failed attempts are counted per IP: 8 in 10 minutes triggers a 10-minute lockout.
- The client IP comes from `CF-Connecting-IP` only when the request arrives from loopback,
  so a remote client cannot spoof it.
- The request body is capped at 4 KiB before it is read.
- Password hashes are argon2id (`m=32768, t=3, p=2`) or bcrypt. Plain SHA-256 is rejected.
- Comparison is constant time.

## Content safety

- Raw HTML in Markdown is sanitized with an allowlist: no `<script>`, no event handlers, no
  `<iframe>`, no inline styles.
- `.html` and `.js` are not in the static allowlist. Adding them re-introduces same-origin
  script execution and should be a deliberate decision.
- SVG files are served as `Content-Disposition: attachment` with a sandboxing CSP, so an SVG
  cannot run script in the site's origin.
- Mermaid renders inside `<iframe sandbox="allow-scripts">` without `allow-same-origin`, which
  gives it an opaque origin.

## Response headers

```text
Content-Security-Policy: default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline';
  img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self';
  form-action 'self'; base-uri 'none'; frame-ancestors 'none'
X-Content-Type-Options: nosniff
Referrer-Policy: no-referrer
X-Frame-Options: DENY
Cross-Origin-Opener-Policy: same-origin
```

HTML responses are `Cache-Control: no-store`. Files served from a mount (images, PDFs) are
`private, max-age=300` so a shared proxy cannot hand one visitor's document to another. The
embedded UI assets are `public, max-age=31536000, immutable`, which is safe because their URL
carries a content hash.

## Path safety

Every request path is cleaned, resolved with `EvalSymlinks`, and checked with `filepath.Rel`
against the mount root. A symlink pointing outside the root is treated as missing. The static
asset route uses a fixed filename whitelist instead of a filesystem walk.

## Secrets on disk

| File | Mode | Notes |
| --- | --- | --- |
| `secret_file` | `0600` | 32-byte hex HMAC key, generated if missing |
| `tunnel.token_file` | `0600` | Cloudflare tunnel token |
| `password_file` | `0600` | Password or hash |
| `state_dir/epoch` | `0600` | Rotation counter for revocation |
| `state_dir/passwords/*.pass` | `0600` | Generated passwords |

A file that is readable or writable by other users is rejected at startup rather than
silently accepted.

## Network posture

The server binds `127.0.0.1` by default. Listening on any other address requires an explicit
`public_url`, which prevents accidentally exposing an unauthenticated-looking service on a
LAN interface.

The tunnel token is passed to `cloudflared` through the environment, never on the command
line, so it does not appear in `ps` output.

## Out of scope

- Per-user accounts, groups, and audit logs.
- Per-directory permissions inside a site. Use separate hostnames instead.
- Rate limiting at the network edge; Cloudflare handles that better.
- Protection against a compromised host or a reader who forwards their password.
