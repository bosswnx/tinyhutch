# TinyFM Documentation

**English** | [中文](README.zh.md)

TinyFM is a single-binary, zero-dependency, self-hosted, minimal file manager. It exposes the files in a directory through a browser interface for browsing and modification, with simple access control.

TinyFM features:

- One-step single-binary deployment, fully dependency-free, with excellent performance.
- Independent access control for each directory and file.
- File downloads through links, suitable for scripts.
- Mobile layout, dark mode, and Chinese/English support.

## Quick start

```bash
# Use the current directory as the working directory by default
./tinyfm

# Specify the working directory
./tinyfm <path>

# Specify a config file
./tinyfm --config <config>
```

## Configuration

If no config file is specified, TinyFM reads `config.json` from the working directory by default. If the file is not found, a new config file is created. After the config file is updated, restart TinyFM to load the latest configuration.

**Note: All configuration fields must be present, or TinyFM will not start.**

The config template is `config.example.json`, with the following fields:

| Field | Description |
|---|---|
| `site_name` | Browser title and top-bar name |
| `listen_host` | Listen address |
| `listen_port` | Listen port |
| `admin_password` | Admin password; when empty, clicking login logs in directly |
| `session_hours` | Session lifetime (hours) |
| `max_upload_mb` | Per-file upload limit (MB) |
| `show_hidden` | Show hidden files |
| `log_level` | Log level |
| `trust_proxy` | Trust `X-Forwarded-For`; enable only behind a trusted reverse proxy |
| `insecure_cookie` | Use a non-Secure cookie for local HTTP testing; keep `false` in production |
| `rules` | Access rule list |

## Build

Requires Go ≥ 1.25.

```bash
make build    # local binary at dist/tinyfm
make test     # unit + integration tests
make dist     # linux/amd64, linux/arm64, darwin/arm64
```
