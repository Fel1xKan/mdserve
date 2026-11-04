# CLI reference

```text
mdserve <command> [flags]
```

## Commands

| Command | What it does |
| --- | --- |
| `mdserve serve` | Start the server. |
| `mdserve share [--host H] [--ttl 1h]` | Mint a single-use share code for a host. |
| `mdserve revoke` | Rotate the epoch, invalidating every session and code. |
| `mdserve passwd [--length 20]` | Generate a password and its argon2id hash. |
| `mdserve config [--json]` | Print the effective configuration and the source of each value. |
| `mdserve version [--json]` | Print version, commit, and build date. |
| `mdserve uninstall [--yes] [--purge] [--keep-secrets]` | Remove local state. Previews unless `--yes` is given. |

`uninstall` removes the secret, the epoch, and generated password files. `--purge` also deletes
the configuration file; `--keep-secrets` leaves the secret and epoch in place. It never touches
the tunnel or the documents.

## serve flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--root DIR` | current directory | Document root. |
| `--listen ADDR` | `127.0.0.1:8787` | Listen address. |
| `--port N` | — | Change only the port. |
| `--config FILE` | auto | Configuration file; must exist. |
| `--public-url URL` | empty | External URL. Required when listening off-loopback. |
| `--base-path /notes` | empty | Serve under a sub-path. |
| `--password-file FILE` | empty | Password file for mounts without their own `auth`. |
| `--tunnel none\|named` | `none` | Tunnel mode. |
| `--tunnel-token-file FILE` | empty | Cloudflare tunnel token. |
| `--tunnel-config FILE` | empty | Your own `cloudflared` config instead of a token. |
| `--tunnel-host HOST` | empty | Public hostname for the tunnel. |
| `--allow-ext .pdf` | — | Append a static extension. Repeatable. |
| `--exclude NAME` | — | Append a path to skip while scanning. Repeatable. |
| `--hidden GLOB` | — | Append a glob to hide from the tree. Repeatable. |
| `--log-level L` | `info` | `debug`, `info`, `warn`, `error`. |
| `--log-format F` | `text` | `text` or `json`. |

Flags override environment variables, which override the YAML file. List flags accept commas or
repeated use.

## Examples

```bash
# Local preview of the current directory
mdserve serve

# A specific folder on another port
mdserve serve --root ~/notes --port 8080

# Behind a named tunnel
mdserve serve --root ~/notes \
  --tunnel named \
  --tunnel-token-file /etc/mdserve/tunnel.token \
  --tunnel-host docs.example.com

# Where did this value come from?
mdserve config | grep -i listen

# One-time access for a reviewer
mdserve share --host docs.example.com --ttl 2h
```

## Exit behaviour

`serve` exits non-zero on a configuration error, a port it cannot bind, or a missing
`cloudflared` when a tunnel is requested. A tunnel that dies later is restarted, not fatal.

`SIGINT` and `SIGTERM` stop the server gracefully, with a five-second deadline for in-flight
requests.
