# Changelog

Notable changes to TinyHutch. The format loosely follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.1] - 2026-10-19

Docker deployment, a unified default port, and an English command line.

### Added

- **Docker deployment.** A multi-stage `Dockerfile`, a `docker-compose.yml` and
  an `.env.example` bring the server up with `docker compose up -d --build`.
  The container takes ownership of `/data` as root, then drops to the
  unprivileged `tinyhutch` user (uid/gid 1000) and runs with
  `no-new-privileges`; a health check polls the configured port. On first start
  it generates an admin password and prints it to the container log unless
  `TINYHUTCH_ADMIN_PASSWORD` is set.
- `make docker-build`, `make docker-up` and `make docker-down`.

### Changed

- **The default listen port is now 7500**, unified across the binary,
  `config.example.json`, the Docker image and the docs. Existing installations
  are unaffected — `config.json` always records `listen_port` explicitly, so
  only fresh installs pick up the new default.
- **The CLI, the logs and the API error messages are now English.** This covers
  `--help`, the startup banner, every `slog` line, config validation errors, and
  the `message` field of API errors — everything you see before reaching the web
  UI, or when driving the API from `curl` or a script. The web UI itself is
  unchanged and still ships in English and Chinese.

### Documentation

- A Docker section in both READMEs: the environment variables, the fact that
  `.env` only applies while `config.json` is being created, the bind-mount
  permission fix for Linux, and the HTTPS reverse proxy settings
  (`TINYHUTCH_TRUST_PROXY`, `TINYHUTCH_INSECURE_COOKIE`, and raising the
  proxy's request-body limit to match `max_upload_mb`).

## [0.2.1] - 2026-10-18

### Changed

- **Renamed the project from TinyFM to TinyHutch**, along with the binary name
  and the runtime files. Config file keys are unchanged.
- Release assets are now raw executables named
  `tinyhutch_{linux,darwin}_{amd64,arm64}` with no version in the file name, so
  `/releases/latest/download/...` URLs stay stable across releases. Checksums
  are published as `SHA256SUMS`.

## [0.1.1] - 2026-10-18

First public release.

### Added

- Browsing, upload, rename, move and delete, per-file download, multi-select
  zip download, and a one-click undo for the last delete.
- Per-path password locks: locking a directory locks everything below it, and
  unlocking one path never unlocks another.
- An English/Chinese UI with a language picker, dark mode and a mobile layout.

[0.3.1]: https://github.com/bosswnx/tinyhutch/compare/v0.2.1...v0.3.1
[0.2.1]: https://github.com/bosswnx/tinyhutch/compare/v0.1.1...v0.2.1
[0.1.1]: https://github.com/bosswnx/tinyhutch/releases/tag/v0.1.1
