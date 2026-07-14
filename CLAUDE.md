# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A single-file Go HTTP server that acts as a local stand-in for `https://mise-versions.jdx.dev`. It is wired into mise via `[settings.url_replacements]` so that mise fetches per-tool version lists from a local checkout of the [`jdx/mise-versions`](https://github.com/jdx/mise-versions) repo instead of the public host.

## Architecture (the whole picture)

- `main.go` is the entire implementation (~40 lines). It serves `docs/<tool>.toml` from an upstream mise-versions checkout at the URL path `/data/<tool>.toml` using `http.FileServer` + `http.StripPrefix`, forcing `Content-Type: application/toml`. Plus a `/health` endpoint.
- **The core insight** (see README "Key finding"): production serves `/data/*.toml` as a byte-for-byte copy of `docs/*.toml` — the upstream build does no transformation. So the server is just a static file mapping of `/data/` → `docs/`. No parsing, no rewriting.
- **Intentionally out of scope:** the GitHub release mirror (`GET /api/github/...`) and analytics stub (`POST /api/tools/...`). mise falls back to `api.github.com` directly, so these are unnecessary. If added later, they'd be thin passthrough handlers ("full local mirror"). Don't add them without reason.
- Config knobs are two env vars: `DOCS_DIR` (path to the `docs/` folder, default `docs`) and `ADDR` (listen address, default `:8080` — must match the `url_replacements` target).

## Running

Go is provided via mise but no version is selected in this repo's `mise.toml` (which only pins `dive`), so invoke Go explicitly:

```bash
DOCS_DIR=/path/to/jdx/mise-versions/docs mise x go@1.26.4 -- go run .
# or plain `go run .` if you have a go version selected
```

Docker bakes the pre-warmed release bundle (`docs/` + warmed `/api/github` `cache/`)
from the rolling `cache-latest` GitHub release, so the image is a self-contained,
offline-capable mirror. `cache-latest` is a rolling tag — build with `--no-cache` (or bump
the `BUNDLE_REF`/`BUNDLE_REPO` build args) to pull a newer bundle:

```bash
docker compose up --build   # supports `docker compose watch` (rebuilds on main.go change)
```

## Verifying a change

```bash
# served bytes must equal the source docs file
cmp <(curl -s http://localhost:8080/data/lazygit.toml) /path/to/jdx/mise-versions/docs/lazygit.toml

# end-to-end — config must be trusted first
mise trust ./mise.toml
mise use -v lazygit    # expect no "Connection refused" / retry warnings for /data/*
```

## Gotchas

- Any project using `[settings.url_replacements]` must be `mise trust`-ed first, or mise errors before ever contacting this server.
- mise **caches** version lists, so a successful fetch means subsequent runs may not re-hit `/data/` — that's expected, not a failure.
- No request logging (stdlib `http.FileServer`). Add logging middleware if you need to watch requests.
- Keep the served data fresh by `git pull`-ing the mise-versions checkout (or bumping the Docker commit pin).
