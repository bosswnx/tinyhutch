# TinyHutch

**English** | [中文](README.zh.md)

[![Release](https://img.shields.io/github/v/release/bosswnx/tinyhutch?sort=semver)](https://github.com/bosswnx/tinyhutch/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go 1.25+](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)

TinyHutch is a **simple, single-binary, self-hosted file manager**. Point it at a directory and manage those files from any browser — upload, rename, move, download, and share — with password locks for individual files and directories. No database, no runtime, no dependencies.

![TinyHutch file manager](img.png)

## Why TinyHutch

- **One binary, zero dependencies.** A ~7 MB Go executable for Linux and macOS (amd64/arm64). No Node, PHP, Python, or database to install.
- **Simple by design.** Run `./hutch /srv/files` and you are done; `config.json` is created on first start.
- **Per-path password locks.** Lock a file or a whole directory subtree with its own password. Unlocking one path does not unlock the rest.
- **Built for sharing.** Copy a download link for any file, or zip and download a whole folder. Links work in scripts and with `curl`.
- **Comfortable UI.** Mobile layout, dark mode, and English/Chinese support.
- **Practical file operations.** Upload with a configurable size limit, drag to move, rename, create folders, undo a delete right after it happens, and filter the current directory.

## Quick start

Download the binary for your platform from [Releases](https://github.com/bosswnx/tinyhutch/releases/latest), then:

```bash
chmod +x hutch_linux_amd64
./hutch_linux_amd64 /path/to/files
```

TinyHutch serves the current directory by default and listens on `127.0.0.1:5000`, creating `config.json` in the served directory.

```bash
./hutch                      # serve the current directory
./hutch /srv/files           # serve a specific directory
./hutch --config my.json     # use a specific config file
```

Open <http://127.0.0.1:5000>.

> Running on a remote server? Set `listen_host` to `0.0.0.0` and put TinyHutch behind a TLS reverse proxy. Keep `admin_password` set and `insecure_cookie` off in production.

## Configuration

`config.json` is read from the served directory (or from `--config`). Every field must be present, or TinyHutch will not start. The template is [`config.example.json`](config.example.json).

| Field | Description |
|---|---|
| `site_name` | Browser title and top-bar name |
| `listen_host` | Listen address |
| `listen_port` | Listen port |
| `admin_password` | Admin password; when empty, clicking login signs in directly |
| `session_hours` | Session lifetime (hours) |
| `max_upload_mb` | Per-file upload limit (MB) |
| `show_hidden` | Show hidden files |
| `log_level` | Log level |
| `trust_proxy` | Trust `X-Forwarded-For`; enable only behind a trusted reverse proxy |
| `insecure_cookie` | Use a non-Secure cookie for local HTTP testing; keep `false` in production |
| `rules` | Access rule list |

After changing the config file, restart TinyHutch to load it.

### Locking paths

Add rules to require a password for specific paths:

```json
"rules": [
  { "path": "/private", "password": "secret" },
  { "path": "/reports/q4.pdf", "password": "another-secret" }
]
```

A locked directory also locks everything below it, and rule paths cannot overlap. Unlocking a path only unlocks that path and its children for the current session.

## Build

Requires Go ≥ 1.25.

```bash
make build    # local binary at dist/hutch
make test     # unit + integration tests
make dist     # linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
make snapshot # local GoReleaser dry run into .goreleaser/
```

## Release

Releases follow Semantic Versioning and are driven by Git tags. Pushing a `v*` tag runs GoReleaser in GitHub Actions, builds the Unix executables, and publishes them together with `SHA256SUMS` on GitHub Releases.

```bash
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

The current version is `0.2.0`; `make build` and `make dist` inject it through `-ldflags`. Release assets are named `hutch_<os>_<arch>` without a version, so `/releases/latest/download/` URLs stay stable across releases.

## License

[MIT](LICENSE)
