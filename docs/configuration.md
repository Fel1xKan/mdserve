# Configuration reference

mdserve reads YAML, with command-line flags and environment variables taking precedence.

## Where the file is found

First match wins:

1. the path passed to `--config` (it must exist);
2. `./mdserve.yaml`, `./mdserve.yml`, `./.mdserve.yaml`;
3. `$XDG_CONFIG_HOME/mdserve/config.yaml`;
4. `~/.config/mdserve/config.yaml`.

If none exists, built-in defaults are used and the current directory becomes the only mount.

## Precedence and merging

```text
flags  >  environment  >  YAML file  >  defaults
```

- Scalars replace the value below them.
- `allow_ext`, `exclude`, and `hidden` append instead of replacing.
- `mounts` is replaced as a whole: defining mounts in YAML discards the implicit
  current-directory mount.
- Unknown keys are a startup error, so a typo fails loudly instead of being ignored.

`mdserve config` prints the effective configuration and marks the source of every value;
`mdserve config --json` does the same as JSON.

## Skeleton

```yaml
version: 1

server:
  listen: 127.0.0.1:8787

mounts:
  - name: docs
    host: docs.example.com
    root: /srv/docs
    auth:
      password_hash: "$argon2id$..."

auth:
  state_dir: ~/.local/state/mdserve

tunnel:
  mode: named
  token_file: /etc/mdserve/tunnel.token
  hostname: docs.example.com
```

## server

| Key | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:8787` | Listen address. Anything other than a loopback address requires `public_url`. |
| `public_url` | empty | External URL, used for share links and the `Secure` cookie flag. Inferred from the tunnel URL when possible. |
| `base_path` | empty | Serve under a sub-path such as `/notes`. Links, cookies, and assets all carry the prefix. |
| `refresh` | `10s` | How often the directory tree is rebuilt. |
| `max_file_bytes` | `4194304` | Documents larger than this are rejected with `413`. |
| `allow_ext` | see below | Extra extensions served as static files. Values append. |
| `exclude` | `.git`, `node_modules`, `.obsidian`, `.idea`, `.vscode` | Paths never scanned. Values append. |
| `hidden` | empty | Globs kept out of the tree, listings, and search but still reachable by URL. Values append. |
| `language` | `zh` | Reserved for UI localization; only Chinese strings ship today. |
| `log_level` | `info` | `debug`, `info`, `warn`, or `error`. |
| `log_format` | `text` | `text` or `json`. JSON is easier to ship to a log store. |

Default `allow_ext`: `.png .jpg .jpeg .gif .svg .webp .avif .ico .pdf .zip .csv .txt .json
.yaml .yml .toml .mp4 .webm .mp3 .wav .xlsx .docx .pptx .woff2 .woff`.

`.html` and `.js` are deliberately absent: serving them from the same origin would turn a
document folder into an XSS vector.

## mounts

| Key | Required | Meaning |
| --- | --- | --- |
| `name` | yes | Label used in logs and the banner. |
| `host` | no | Hostname used for routing, without a port. Empty means the default mount; at most one mount may be the default. |
| `root` | yes | Directory to serve. Must exist unless the command skips the check (`config`, `uninstall`). |
| `auth.password_hash` | no | argon2id or bcrypt hash. Takes precedence over `password_file`. |
| `auth.password_file` | no | File containing a password or a hash. Must not be readable by other users. |

Omitting `auth` generates a 20-character password on first start, stores it in
`<state_dir>/passwords/<hash>.pass` with mode `0600`, and prints it once.

`--password-file` applies a single file to every mount that has no `auth` block of its own.

### Renaming a host

`mounts[].host` is the source of truth for routing, but Cloudflare keeps its own copy: the
DNS record and the tunnel ingress rule. Changing the host in YAML does not touch either, so
the old name keeps resolving and starts returning `421`.

mdserve remembers the hostnames it served in `<state_dir>/hosts.json`. On the next start it
warns once about names that disappeared:

```
level=WARN msg=旧域名已不在配置中 hosts=old.example.com
```

It never edits Cloudflare. Remove the old DNS record and ingress rule yourself, then restart
to refresh the snapshot. The same warning fires at runtime (once per host) if traffic still
arrives for a name no mount claims.

## auth

| Key | Default | Meaning |
| --- | --- | --- |
| `secret_file` | `~/.config/mdserve/secret` | 32-byte hex HMAC key, created with mode `0600` when missing. |
| `state_dir` | `~/.local/state/mdserve` | Holds the epoch and generated passwords. |
| `cookie_name` | `mdserve_s` | Session cookie name. Cookies are isolated by host anyway. |
| `cookie_ttl` | `8h` | Absolute session lifetime. |
| `idle_ttl` | `30m` | Idle lifetime; each successful request renews it. |
| `share_ttl` | `1h` | Lifetime of a share code. |
| `rate_limit.attempts` | `8` | Failed logins allowed per window. |
| `rate_limit.window` | `10m` | Window length. |
| `rate_limit.lockout` | `10m` | Lockout after the limit is hit. |

Durations accept Go syntax (`500ms`, `90s`, `12h`) or a bare number, which means seconds.

## tunnel

| Key | Default | Meaning |
| --- | --- | --- |
| `provider` | `cloudflared` | Only Cloudflare is supported. |
| `mode` | `none` | `none` or `named`. |
| `binary` | `cloudflared` | Name on `PATH` or an absolute path. Never downloaded. |
| `token_file` | empty | Token file, mode `0600`. Mutually exclusive with `config`. |
| `config` | empty | Your own `cloudflared` config file. |
| `hostname` | empty | Public hostname, used for logging and share links. Required for `named`. |

`named` requires either `token_file` or `config`, plus `hostname`. There is no quick-tunnel
mode: an ephemeral `trycloudflare.com` hostname cannot be routed per mount.

## Environment variables

Environment variables are a fixed whitelist. Passwords are never read from the environment.

| Variable | Key |
| --- | --- |
| `MDSERVE_LISTEN` | `server.listen` |
| `MDSERVE_PUBLIC_URL` | `server.public_url` |
| `MDSERVE_BASE_PATH` | `server.base_path` |
| `MDSERVE_REFRESH` | `server.refresh` |
| `MDSERVE_MAX_FILE_BYTES` | `server.max_file_bytes` |
| `MDSERVE_ALLOW_EXT` | `server.allow_ext` (comma separated, appends) |
| `MDSERVE_EXCLUDE` | `server.exclude` (comma separated, appends) |
| `MDSERVE_HIDDEN` | `server.hidden` (comma separated, appends) |
| `MDSERVE_LANGUAGE` | `server.language` |
| `MDSERVE_LOG_LEVEL` | `server.log_level` |
| `MDSERVE_LOG_FORMAT` | `server.log_format` |
| `MDSERVE_SECRET_FILE` | `auth.secret_file` |
| `MDSERVE_STATE_DIR` | `auth.state_dir` |
| `MDSERVE_COOKIE_NAME` | `auth.cookie_name` |
| `MDSERVE_COOKIE_TTL` | `auth.cookie_ttl` |
| `MDSERVE_IDLE_TTL` | `auth.idle_ttl` |
| `MDSERVE_SHARE_TTL` | `auth.share_ttl` |
| `MDSERVE_RATE_ATTEMPTS` | `auth.rate_limit.attempts` |
| `MDSERVE_RATE_WINDOW` | `auth.rate_limit.window` |
| `MDSERVE_RATE_LOCKOUT` | `auth.rate_limit.lockout` |
| `MDSERVE_TUNNEL_MODE` | `tunnel.mode` |
| `MDSERVE_TUNNEL_BINARY` | `tunnel.binary` |
| `MDSERVE_TUNNEL_TOKEN_FILE` | `tunnel.token_file` |
| `MDSERVE_TUNNEL_CONFIG` | `tunnel.config` |
| `MDSERVE_TUNNEL_HOSTNAME` | `tunnel.hostname` |

## Sub-path deployment

```yaml
server:
  base_path: /notes
```

Every internal link, the cookie path, and the asset URLs gain the prefix. `/notes` redirects
to `/notes/`, and anything outside the prefix returns `404`.

## Local-only example

```yaml
server:
  listen: 127.0.0.1:8787

mounts:
  - name: notes
    root: ~/notes
    auth:
      password_file: ~/.config/mdserve/notes.pass
```

## Two sites, one process

```yaml
mounts:
  - name: docs
    host: docs.example.com
    root: /srv/docs
  - name: handbook
    host: handbook.example.com
    root: /srv/handbook
```

Both hosts need a public hostname in the Cloudflare tunnel pointing at `localhost:8787`.

## Validation

Startup fails, rather than guessing, when:

- a key is unknown or a value has the wrong type;
- `version` is newer than the binary supports;
- `root` does not exist (except for `config` and `uninstall`);
- two mounts share a host, or more than one mount has an empty host;
- `listen` is not loopback and `public_url` is empty;
- `tunnel.mode` is `named` without a token file or config, or without `hostname`.
