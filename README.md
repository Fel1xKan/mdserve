<div align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset=".github/logo-light.svg">
    <img alt="mdserve" src=".github/logo-light.svg" width="360">
  </picture>
</div>

<div align="center">

[![License: MIT][license-shield]][license-url]
[![Latest release][release-shield]][release-url]
[![CI][ci-shield]][ci-url]
[![Go 1.26][go-shield]][go-url]
[![Platforms][platform-shield]](#install)

</div>

<div align="center">
  <a href="#quick-start">Quick Start</a> &middot;
  <a href="#usage">Usage</a> &middot;
  <a href="docs/configuration.md">Configuration</a> &middot;
  <a href="docs/security.md">Security</a> &middot;
  <a href="AGENTS.md">Agent Setup</a> &middot;
  <a href="README-CN.md">简体中文</a>
</div>

> A folder of Markdown, served behind a password. One binary, no build step, no database.

---

## Why mdserve?

Your documentation is already Markdown. Getting it to a URL usually means adding a static site
generator, a build step, and a deploy pipeline. A wiki adds a database and an editor you do not
need. A file browser shows a tree of files instead of a page someone can read.

mdserve serves the folder directly: one command, one password, and an optional Cloudflare
tunnel when the folder has to leave the machine. It is built for small, trusted audiences — a
team handbook, client documentation, personal notes — not for wikis, file managers, or
multi-tenant services.

## Features

- **Publish without a build.** Point it at a directory and Markdown is rendered on request. Edit
  a file, refresh the page, done.
- **One process, many sites.** Route by hostname, each site with its own directory and password.
  An unknown host gets `421`, never someone else's content.
- **Share access, not credentials.** Mint a single-use code that expires in an hour, and kill
  every session with `mdserve revoke`.
- **Read-only by design.** Nothing is written to your folder. HTML files are not served, inline
  HTML is sanitized, and SVG downloads instead of rendering.
- **Self-contained.** CSS, fonts, and Mermaid are embedded and pre-compressed. No CDN, no
  runtime downloads, works on a host with no outbound access.
- **Made for reading.** Server-rendered tree, table of contents, and full-text search; pages
  work with JavaScript disabled.

## When to Use

| Reach for mdserve when | Look elsewhere when |
| --- | --- |
| You have a Markdown folder and a handful of readers | You need uploads, WebDAV, or a file manager |
| You want one password per audience, not a user database | You need per-user accounts, SSO, or audit logs |
| The machine can stay behind a tunnel with no open ports | You have thousands of documents and need ranked search |

In the right column, dufs, miniserve, and filebrowser handle file management; Cloudflare Access
or a documentation platform handles identity and audit.

## Quick Start

```bash
cd ~/notes
mdserve serve
```

It prints the local URL and a generated password, stored under
`~/.local/state/mdserve/passwords/` with mode `0600`. Open the URL and enter it.

To choose your own password instead:

```bash
mdserve passwd   # prints the password and an argon2id hash for auth.password_hash
```

## Install

```bash
# Linux x86_64 — swap the asset name for your platform
VER=$(curl -fsSL https://api.github.com/repos/Fel1xKan/mdserve/releases/latest \
      | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')
curl -fsSL -o mdserve \
  "https://github.com/Fel1xKan/mdserve/releases/latest/download/mdserve-$VER-linux-amd64"
install -m 755 mdserve ~/.local/bin/mdserve
```

With Go 1.26 or newer:

```bash
go install github.com/Fel1xKan/mdserve@latest
```

| Platform | Asset |
| --- | --- |
| Linux | `mdserve-<version>-linux-{amd64,arm64}` |
| macOS | `mdserve-<version>-darwin-{arm64,amd64}` |
| Windows | `mdserve-<version>-windows-{amd64,arm64}.exe` |

macOS blocks unsigned binaries on first run: `xattr -d com.apple.quarantine ./mdserve`.

Only two optional dependencies: `cloudflared` for the tunnel, and Go if you build from source.

## Usage

### Serve a folder

```bash
mdserve serve --root ~/notes --port 8080
```

### Two sites in one process

```yaml
mounts:
  - name: docs
    host: docs.example.com
    root: /srv/docs
    auth:
      password_file: /etc/mdserve/docs.pass
  - name: handbook
    host: handbook.example.com
    root: /srv/handbook
    auth:
      password_hash: "$argon2id$..."
```

Each hostname needs its own public hostname in the Cloudflare tunnel, all pointing at
`localhost:8787`.

### Put it online

```bash
mdserve serve --root ~/notes \
  --tunnel named \
  --tunnel-token-file /etc/mdserve/tunnel.token \
  --tunnel-host docs.example.com
```

`cloudflared` runs as a supervised child process, restarted with backoff if it exits. The token
reaches it through the environment, so it never shows up in `ps`.

### Share and revoke

```bash
mdserve share --host docs.example.com   # single-use code, valid for one hour
mdserve revoke                          # invalidate every session and code
```

→ [CLI reference](docs/cli.md): every command, every flag, and what `mdserve config` reports.

## How It Works

A request is routed by `Host` to a mount, resolved against the root with symlink checks,
rendered from Markdown to sanitized HTML, and served with a host-bound session cookie.

→ [How mdserve works](docs/how-it-works.md)

## Configuration

YAML at `./mdserve.yaml` or `~/.config/mdserve/config.yaml`; flags beat environment variables,
which beat the file. Passwords come from a hash or a `0600` file, never plaintext, and
`mdserve config` shows the effective value and where it came from.

→ [Configuration reference](docs/configuration.md)

## Security

Loopback only, read-only, sanitized HTML, rate-limited login, host-bound sessions, and
one-command revocation. The password keeps crawlers and casual visitors out; it is not a
defense against a motivated attacker.

→ [Security model](docs/security.md)

## Contributing

Issues and pull requests are welcome. Run `make test` and `make vet` before sending a patch.

## License

MIT — see [LICENSE](LICENSE).

---

[license-shield]: https://img.shields.io/badge/license-MIT-blue.svg
[license-url]: LICENSE
[release-shield]: https://img.shields.io/github/v/release/Fel1xKan/mdserve
[release-url]: https://github.com/Fel1xKan/mdserve/releases
[ci-shield]: https://img.shields.io/github/actions/workflow/status/Fel1xKan/mdserve/ci.yml
[ci-url]: https://github.com/Fel1xKan/mdserve/actions/workflows/ci.yml
[go-shield]: https://img.shields.io/badge/Go-1.26-00ADD8.svg
[go-url]: https://go.dev
[platform-shield]: https://img.shields.io/badge/platforms-linux%20%7C%20macOS%20%7C%20Windows-lightgrey.svg
