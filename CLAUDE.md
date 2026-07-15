# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A small Go HTTP server that acts as a local stand-in for `https://mise-versions.jdx.dev`, wired into mise via `[settings.url_replacements]` (or `MISE_URL_REPLACEMENTS`). It serves per-tool version lists, the cpython asset list, and a cached GitHub release mirror — and can emit a **path-mirrored static tree** that GitHub Pages serves without a running server.

## Architecture (the whole picture)

- **`main.go`** (single file) wires three routes + `/health` behind a request-logging middleware:
  - `GET /data/<tool>.toml` — the version list, served from `DOCS_DIR` via `http.FileServer` (+ forced `application/toml`). Production serves these as a **byte-for-byte copy** of `docs/*.toml` in jdx/mise-versions, so it's a plain file mapping (see README "Key finding").
  - `GET /tools/<name>.gz` — `toolsGzHandler` reconstructs the gzipped cpython asset list from `DOCS_DIR/<name>.toml`'s `[versions]` keys (upstream 404s this path). **A 404 here is fatal to `core:python`.**
  - `GET /api/github/*` — the `mirror` disk-caching reverse proxy. It records each 2xx at a path that mirrors the request (`api/github/repos/.../latest` + a `<path>.meta` sidecar for headers) so the tree is servable as-is by a static host; the body file is the exact upstream bytes. HIT/MISS/REVALIDATED/STALE/BYPASS/502 flow with ETag/`Last-Modified`/TTL revalidation; `mirrorPaths` guards against `..` traversal.
- **Static mirror / Pages**: the tree lives **at the repo root on `main`** (`data/`, `tools/`, `api/`, `.nojekyll`) and Pages serves it from `/`. `scripts/sync-docs.sh` pulls jdx/mise-versions `docs/` into `data/`; `scripts/build-site.sh` reconstructs `tools/*.gz`; the server fills `api/github/`. `.github/workflows/warm-cache.yml` (every 2h) warms on amd64+arm64 (the committed `api/` seeds the cache → incremental), merges, and commits the refreshed tree back to `main`.
- **Config knobs (env):** `DOCS_DIR` (default `docs`; loop uses `./data`), `ADDR` (default `:8080`), `CACHE_DIR` (tree root, default `cache`; loop uses `.`), `CACHE_TTL` (default `1h`), `UPSTREAM` (default the real host). One cache layout — path-mirrored — no toggle.

## Running

Go is provided via mise but no version is selected in this repo's `mise.toml` (which only pins `dive`), so invoke Go explicitly:

```bash
DOCS_DIR=/path/to/jdx/mise-versions/docs mise x go@1.26.4 -- go run .
# repo-root loop (records into ./api, serves ./data): scripts/serve.sh
DOCS_DIR=./data CACHE_DIR=. mise x go@1.26.4 -- go run .
```

Docker bakes the in-repo tree (`data/ + tools/ + api/`) onto distroless static, so the image is a self-contained, offline-capable mirror (`DOCS_DIR=/app/data`, `CACHE_DIR=/app`). Build after the tree is populated (via the loop or a workflow commit). The `builder` stage builds the **whole module**, so new `.go` files are picked up automatically.

```bash
docker compose up --build   # watches main.go
```

## Verifying a change

```bash
mise x go@1.26.4 -- go test -race ./...   # main_test.go covers the mirror proxy

# served bytes must equal the source docs file
cmp <(curl -s http://localhost:8080/data/lazygit.toml) /path/to/jdx/mise-versions/docs/lazygit.toml

# the recorded body IS the request path, servable as-is
curl -s http://localhost:8080/api/github/repos/jesseduffield/lazygit/releases/latest >/dev/null
cat "$CACHE_DIR/api/github/repos/jesseduffield/lazygit/releases/latest"   # exact upstream bytes

# end-to-end via the env var (no `mise trust` needed)
MISE_URL_REPLACEMENTS='{"https://mise-versions.jdx.dev":"http://localhost:8080"}' mise use -v lazygit
```

## Gotchas

- A `[settings.url_replacements]` project must be `mise trust`-ed first; the `MISE_URL_REPLACEMENTS` **env var** needs no trust (used by the scripts and for Pages).
- mise **caches** version lists, so a successful fetch means subsequent runs may not re-hit `/data/` — expected, not a failure. A fresh `docker run --rm` mise container forces the requests.
- The mirror tree (`data/`, `tools/`, `api/`) is **committed** — it's what Pages serves; only `cache/` and the built binary are gitignored. For the local loop, set `CACHE_DIR=.` (or use `scripts/serve.sh`) so `api/` lands at the repo root, not in the ignored `cache/`.
- **GitHub Pages sets its own Content-Type/Encoding** (can't override). Verify a real `mise install` against the Pages URL; the Go server / Docker image are the header-correct fallback.
- Keep data fresh via `scripts/sync-docs.sh` (the workflow runs it every 2h).
