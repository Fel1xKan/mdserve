# AGENTS.md

Setup instructions for an AI agent configuring **mdserve** on a user's machine.
Scope: install, configure, run, verify. Nothing about development.

Typical request: *"Read this file, then expose `~/notes` at `docs.example.com`
and verify it works."*

## Deliverable

- `mdserve` installed and running
- a config file, or a one-line command
- a password the user can type
- proof: URL plus the status codes from §6

Do not claim success before the checks pass.

## How it works

One process serves one or more **mounts**. A mount is a directory + hostname + password.

```text
browser -> Cloudflare -> cloudflared -> mdserve:8787 -> directory
```

- Routing is by `Host`. An unknown host returns 421; there is no fallback.
- mdserve listens on loopback. The tunnel is the only public path.
- Sessions are per-host cookies. Share codes are single-use.

## 1. Check or install

```bash
command -v mdserve && mdserve version --json
```

If missing (Linux x86_64; swap the asset for other platforms):

```bash
VER=$(curl -fsSL https://api.github.com/repos/Fel1xKan/mdserve/releases/latest \
      | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -fsSL -o /tmp/mdserve \
  "https://github.com/Fel1xKan/mdserve/releases/latest/download/mdserve-$VER-linux-amd64"
install -m 755 /tmp/mdserve ~/.local/bin/mdserve
```

Assets are `mdserve-<version>-{linux,darwin,windows}-{amd64,arm64}`, `.exe` on Windows.

## 2. Ask only what you cannot infer

- Is the domain on Cloudflare? A named tunnel requires it.
- Is there a tunnel token? If not, see §3.
- Should it keep running after logout? See §5.

Local-only use needs none of these.

## 3. Public access (skip for local use)

Dashboard: Zero Trust → Networks → Tunnels → **Create a tunnel** → Cloudflared.
Copy the token. Add a **Public Hostname**: `docs.example.com` → HTTP → `localhost:8787`.
Repeat per hostname; every hostname points at the same port.

CLI equivalent:

```bash
cloudflared tunnel login
cloudflared tunnel create mdserve
cloudflared tunnel token mdserve
cloudflared tunnel route dns mdserve docs.example.com
```

Store the token without echoing it:

```bash
sudo install -d -m 700 /etc/mdserve
sudo install -m 600 /dev/null /etc/mdserve/tunnel.token
sudo -e /etc/mdserve/tunnel.token
```

Do not set `originRequest.httpHostHeader`; it collapses every hostname into one.

## 4. Configure

Lookup order: `--config`, `./mdserve.yaml`, `~/.config/mdserve/config.yaml`.

Generate a password and its hash:

```bash
mdserve passwd
```

Public single site:

```yaml
version: 1
server:
  listen: 127.0.0.1:8787
  public_url: https://docs.example.com
mounts:
  - name: docs
    host: docs.example.com
    root: /home/user/notes
    auth:
      password_hash: "$argon2id$..."
tunnel:
  mode: named
  token_file: /etc/mdserve/tunnel.token
  hostname: docs.example.com
```

Add more `mounts` entries for more hostnames. Local only:
`mdserve serve --root ~/notes` prints a generated password once.

Credentials are per mount, via `auth.password_hash` or `auth.password_file`
(mode 0600). There is no global password, no plaintext field, and no env var.
Omit `auth` to auto-generate.

Options worth knowing: `base_path`, `hidden` (glob; still reachable by path),
`exclude` (not scanned), `max_file_bytes`, `log_format: json`.

## 5. Run

Foreground is fine for a demo, but never block the agent's shell:

```bash
mkdir -p ~/.local/state/mdserve
nohup mdserve serve --config ~/.config/mdserve/config.yaml \
  > ~/.local/state/mdserve/serve.log 2>&1 &
```

Persistent, user-level:

```ini
# ~/.config/systemd/user/mdserve.service
[Unit]
Description=mdserve
After=network-online.target

[Service]
ExecStart=%h/.local/bin/mdserve serve --config %h/.config/mdserve/config.yaml
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now mdserve
loginctl enable-linger "$USER"
```

## 6. Verify

Replace `PASSWORD`. All five must match.

```bash
curl -sS -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8787/-/health    # 200

curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: docs.example.com' \
  http://127.0.0.1:8787/                                                   # 303

curl -sS -o /dev/null -w '%{http_code}\n' -c /tmp/md.jar \
  -H 'Host: docs.example.com' \
  -d 'secret=PASSWORD&next=/' http://127.0.0.1:8787/-/login                # 303

curl -sS -b /tmp/md.jar -H 'Host: docs.example.com' \
  http://127.0.0.1:8787/ | grep -c '<article'                              # >= 1

curl -sS -o /dev/null -w '%{http_code}\n' -H 'Host: nope.example.com' \
  http://127.0.0.1:8787/                                                   # 421
```

Public check: `curl -sS -o /dev/null -w '%{http_code}\n' https://docs.example.com/-/health` → 200.

## 7. Share and revoke

```bash
mdserve share --host docs.example.com   # single-use code, 1h by default
mdserve revoke                          # invalidate all sessions and codes
```

## 8. Troubleshooting

| Symptom | Fix |
| --- | --- |
| 421 | Host not in `mounts`. Add it, or give one mount `host: ""` |
| 303 to `/-/login` | Not logged in. Expected |
| 401 on login | Wrong password, or the share code was already used |
| 429 | Rate limited (8 failures per 10 min). Wait, or restart to clear |
| "not a loopback address" | Set `public_url`, or bind `127.0.0.1` |
| "cloudflared not found" | Install it; mdserve never downloads binaries |
| "permissions 0644" | `chmod 600` the token or password file |
| `field ... not found` | Config typo. Unknown keys are fatal |
| 502 or 1033 publicly | Tunnel service must be `http://localhost:8787` |
| Config change ignored | Restart the process |

## 9. Rules

- Never print tokens or passwords. Write files with `install -m 600`.
- Never commit secrets. Never edit the user's Markdown.
- Keep `listen` on loopback.
- Do not run as root.
- Ask before assuming a domain belongs to the user's Cloudflare account.
- Delete session jars such as `/tmp/md.jar` when done.

## 10. Report

```text
Ready:
  URL       https://docs.example.com/
  Directory /home/user/notes (N files)
  Password  ...
  Checks    health 200, login 303, page 200, unknown host 421
  Service   systemd user unit mdserve
```

Say that the password is sensitive.

## 11. Security boundary

The password keeps casual visitors and crawlers out; it is not a defense against a
motivated attacker. For sensitive content, add Cloudflare Access in front.

mdserve has no upload, WebDAV, per-directory permissions, or command execution.
