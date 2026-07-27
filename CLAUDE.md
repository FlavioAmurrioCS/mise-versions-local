# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A **record-and-replay mirror of `https://mise-versions.jdx.dev`**. A small FastAPI reverse
proxy ("recorder") sits in front of the real host; a throwaway container ("invoker") runs
`mise install` against it. Every response mise fetches gets written to disk as a plain file
tree mirroring the URL path, so the repo accumulates a byte-for-byte, offline-servable
snapshot of exactly what mise asks for — no knowledge of mise's API surface is hardcoded.

Why mise-versions matters: mise resolves tool versions from that host, and its
`/api/github/*` routes are a CDN mirror of GitHub release metadata. Without them mise falls
back to `api.github.com` and a batch of tools blows through the unauthenticated
60-req/hour limit.

### Branch context

This branch (`another-story`) has **no commits yet** and is a from-scratch rewrite.
`origin/main` holds an unrelated earlier attempt (a Go server that served a checkout of the
`jdx/mise-versions` repo's `docs/` folder). Don't mix the two — nothing on `main` applies
here, and its `CLAUDE.md`/`README.md` describe the old design.

## Architecture

Two Docker Compose services, both from the same `uv:debian` image, both bind-mounting the
repo at `/root/workspace`:

- **`recorder`** (`recorder/recorder_server.py`, port 8000) — a catch-all
  `/{file_path:path}` proxy to `https://mise-versions.jdx.dev`.
  - `POST`/`PUT`/`DELETE`: pure passthrough, nothing recorded (mise's analytics `POST
    /api/tools/*` lands here).
  - `GET`: writes the body to `./<url path>` and the response headers to
    `./<url path>.meta` (JSON), relative to the working dir — which is the bind-mounted
    repo root. So `/data/fzf.toml` → `./data/fzf.toml`, `/api/github/repos/...` →
    `./api/github/repos/...`. The three top-level mirror roots are `data/`, `tools/`, `api/`.
  - Caching is **HTTP-native**: the stored `etag` is replayed as `If-None-Match`, and a
    `304` is rewritten to `200` with the on-disk body. `date` and `cf-ray` are stripped from
    the stored headers so re-recordings stay diff-clean.
  - If upstream errors and a cached body exists, it serves that body as `200` (offline
    resilience).
- **`invoker-arm64`** (`invoker/main.sh`) — installs mise from `mise.run`, then, for each
  entry in the `tools=(...)` array, installs the latest `number_of_versions` versions
  resolved via `mise ls-remote`. It is pointed at the recorder purely by the
  `MISE_URL_REPLACEMENTS` env var (`{"https://mise-versions.jdx.dev": "http://recorder:8000"}`),
  which sidesteps the `mise trust` requirement that a `mise.toml`-based
  `[settings.url_replacements]` would impose. Its mise state dirs are bind-mounted under
  `dir_cache/arm64/` so a run's cache/data survives between invocations.
  - An `invoker-amd64` service is commented out in `docker-compose.yaml`. Cross-arch matters
    only for `/api/github/.../attestations/<digest>`; release metadata is arch-independent.

Both `dir_cache/` and the mirror roots (`data/`, `tools/`, `api/`) are gitignored **and**
dockerignored — the recordings are build outputs, not sources.

## Commands

```bash
# Full record run: brings up the recorder, then the invoker installs tools through it
docker compose up --build

# Reset — DESTRUCTIVE: rm -rf's dir_cache/* and the data/ tools/ api/ mirrors, then recreates
./create-dirs.sh

# Recorder alone, on the host (syncs into /tmp/foo, not .venv)
./recorder/main.sh

# Lint / format
uv run ruff check
uv run ruff format
```

`ruff` is configured with `select = ["ALL"]` (see `pyproject.toml`); `uv run ruff check`
currently reports 2 pre-existing findings in `recorder_server.py` (`CPY001`, `S104`) — they
are not regressions from your change.

To widen what gets recorded, edit the `tools` array and `number_of_versions` in
`invoker/main.sh` (the commented-out `mise registry | head` loop is the "record everything"
variant).

## Gotchas

- **The point of the recorder is that it is generic.** Resist adding per-endpoint logic —
  if mise starts requesting a new path, it should just start appearing on disk.
- `recorder_server.py` carries a PEP 723 inline-script header *and* is listed in
  `pyproject.toml`; keep both dependency lists in sync if you add an import.
- Writing a top-level path (e.g. `/foo`) hits `os.makedirs("")` and fails — every real mise
  request is nested, so this hasn't bitten yet.
- mise caches version lists locally, so a second run may not re-hit the recorder at all.
  Wipe `dir_cache/` (via `create-dirs.sh`) to force real requests.
