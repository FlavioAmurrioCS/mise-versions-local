# mise-versions-local

A minimal local stand-in for `https://mise-versions.jdx.dev`, used with mise's
`[settings.url_replacements]` to serve tool version lists from a local checkout of the
[`jdx/mise-versions`](https://github.com/jdx/mise-versions) repo.

## Why this exists

mise resolves tool versions by hitting `mise-versions.jdx.dev`. You can redirect that
host to a local server via mise config:

```toml
# mise.toml
[settings.url_replacements]
"https://mise-versions.jdx.dev" = "http://localhost:8080"
```

The things mise strictly needs from that host are the **per-tool version list** at
`GET /data/<tool>.toml`, and — for `core:python` — the **precompiled-cpython asset list**
at `GET /tools/<name>.gz`. This server provides both, from the same `docs/` checkout.

## Key finding: the docs/ folder is enough (no special logic)

- Production serves version lists at `/data/<tool>.toml`.
- Those files are a **byte-for-byte copy** of `docs/<tool>.toml` in the mise-versions
  repo. The upstream build (`scripts/build-static-version-files.js`) just copies
  `docs/*.toml` → `web/public/data/*.toml` with zero transformation.
- So the `/data/` route just maps the URL path onto the repo's `docs/` directory.
- The `/tools/<name>.gz` route is the one bit of logic: production serves a **gzipped,
  newline-separated list** of precompiled cpython asset names. Upstream generates that
  plain-text list, then deletes it — committing only the derived `docs/<name>.toml`, where
  each line is exactly a quoted `[versions]` key. So this server reconstructs the list
  from the `.toml` and gzips it on the fly (verified byte-for-byte vs production). Because
  the two share a source, they stay in sync automatically on `git pull`.

## What mise requests (from a real `mise use -v` trace)

| Endpoint | Handled here? | Notes |
|---|---|---|
| `GET /data/<tool>.toml` | ✅ yes | The version list. Served from `docs/`. |
| `GET /tools/<name>.gz` | ✅ yes | Precompiled-cpython asset list for `core:python`, gzipped. **A 404 here is fatal** — mise aborts the python install with no fallback (unlike `/api/github`). Reconstructed from `docs/<name>.toml`. |
| `GET /api/github/repos/{owner}/{repo}/releases/{latest\|tag}`, `.../attestations/<digest>` | ✅ yes (cached proxy) | GitHub release **mirror**. Proxied to the real `mise-versions.jdx.dev` (a CDN cache, so **not** GitHub's rate-limited API), cached to disk, and revalidated when stale. Serves the cached copy if the upstream is unreachable. See [The /api/github cache](#the-apigithub-cache). |
| `POST /api/tools/<tool>` | ❌ no (404) | Analytics tracking, fire-and-forget. mise ignores the result. |

Why the mirror matters: without it, mise falls back to `api.github.com` directly for each
tool's release metadata, and a batch of ~24 tools blows straight through GitHub's
**unauthenticated 60-req/hour** limit — tools that hit the wall fail outright. Serving the
mirror keeps those lookups off GitHub. (mise still makes a few `releases?per_page=100`
listing calls to `api.github.com` *directly* — never via this host — but a 403 on those is
now just a `WARN`; the install completes from the mirrored release data.)

Only the analytics `POST` is intentionally left unhandled (mise ignores its result).

## Run

Go is available via mise (1.26.4 installed, no global version set here), so invoke it
explicitly. Point `DOCS_DIR` at your mise-versions checkout's `docs/`:

```bash
DOCS_DIR=/Users/flavio/dev/github.com/jdx/mise-versions/docs \
  mise x go@1.26.4 -- go run .

# or plain `go run .` if you have a go version selected
```

Env vars:
- `DOCS_DIR` — path to the `docs/` folder (default `docs`).
- `ADDR` — listen address (default `:8080`, must match the url_replacements target).
- `CACHE_DIR` — where the `/api/github` cache lives (default `cache`).
- `CACHE_TTL` — freshness window before revalidating a cached entry (default `1h`).
  A per-response `Cache-Control: max-age` from the upstream overrides this.
- `UPSTREAM` — host to mirror `/api/github/*` from (default `https://mise-versions.jdx.dev`).

Keep the data fresh by `git pull`-ing the mise-versions repo periodically.

## The /api/github cache

`/api/github/*` is a disk-caching reverse proxy (see `githubProxy` in `main.go`):

- **Serves from disk first.** A fresh entry is returned with `X-Cache: HIT` and makes no
  network call. Freshness follows the upstream's `Cache-Control: max-age` (≈2h), falling
  back to `CACHE_TTL`.
- **Revalidates when stale** via `If-None-Match` / `If-Modified-Since` — `304` refreshes
  the entry in place (`REVALIDATED`), otherwise the new body replaces it (`MISS`).
- **Survives the upstream being down.** If the mirror is unreachable but a cached copy
  exists — even a stale one — that copy is served (`X-Cache: STALE`). Only a cold miss with
  no upstream returns `502`.
- **Portable.** Each entry is a `<sha256>.body` + `<sha256>.meta` pair holding no absolute
  paths, so you can stop the server, copy `CACHE_DIR` to another machine, and it's reused
  as-is. `docker-compose.yaml` bind-mounts `./cache` for exactly this.
- Non-2xx upstream responses (e.g. a `404` for an unmirrored repo) pass straight through
  uncached (`X-Cache: BYPASS`) so mise can still fall back to `api.github.com`.

The disposition tag appears in the request log, e.g.
`GET /api/github/repos/jqlang/jq/releases/jq-1.8.2 -> 200 [HIT]`.

## Pre-warmed bundle (offline / distribution)

A scheduled GitHub Action (`.github/workflows/warm-cache.yml`, every 2h) publishes a ready-
to-serve bundle so a machine can run a fully-populated mirror without warming the cache
itself. Each run: downloads the latest mise-versions `docs/`, installs the tools listed in
`tools.txt` against the server on **amd64 + arm64** (which fills `cache/` through the
proxy), merges the two caches, and uploads `docs/ + cache/ + MANIFEST.json` as a
`.tar.zst` to the rolling **`cache-latest`** release.

- The warming logic lives in **`scripts/warm-cache.sh`**, which is also runnable locally:
  ```bash
  DOCS_DIR=/path/to/jdx/mise-versions/docs CACHE_DIR=./cache scripts/warm-cache.sh
  ```
  It starts the server, `mise install`s `tools.txt` against it in an isolated mise
  environment (scratch data/cache/config dirs, so only `tools.txt` is warmed — not your
  repo or global config), then stops the server.
- **Multiple versions per tool.** Each `tools.txt` line is `name` (latest `VERSIONS`,
  default 1), `name@version` (exact), or `name COUNT` (latest COUNT, resolved via
  `mise ls-remote`); list a tool on several lines to warm specific versions. Set
  `VERSIONS=10` (env locally, or a `VERSIONS` repo variable / the workflow's manual-run
  input) to warm the latest ten of every bare tool. Heavy toolchains multiply CI cost, so
  cap those per line (e.g. `rust 2`) while letting small CLIs keep more history.
- Cross-arch works because `/api/github/.../releases/*` is arch-independent (one run covers
  all arches); only `.../attestations/<digest>` is arch-specific, hence the amd64+arm64
  matrix. Merge is a plain copy — shared entries share a `sha256` key.

**Consuming the bundle** — download and serve it; no rebuild needed:
```bash
curl -fsSL -o bundle.tar.zst \
  https://github.com/<owner>/mise-versions-local/releases/download/cache-latest/mise-versions-bundle.tar.zst
mkdir bundle && tar --zstd -xf bundle.tar.zst -C bundle
DOCS_DIR=bundle/docs CACHE_DIR=bundle/cache go run .   # or the Docker image with these mounted
```
Stale bundle entries revalidate against the upstream on use and fall back to the bundled
copy when offline — the same `HIT`/`STALE` behaviour described above.

> **Prerequisite:** this repo has no git remote yet. Push it to GitHub and enable Actions
> for the schedule to run; the workflow needs `contents: write` (already declared) to
> publish the release.

## Verify

```bash
# /data: byte-identical to source for the exact path mise requests
cmp <(curl -s http://localhost:8080/data/lazygit.toml) \
    /Users/flavio/dev/github.com/jdx/mise-versions/docs/lazygit.toml

# /tools: our gzipped list decompresses to the same bytes production serves
P=python-precompiled-aarch64-unknown-linux-gnu.gz
diff <(curl -s "http://localhost:8080/tools/$P" | gzip -dc) \
     <(curl -s "https://mise-versions.jdx.dev/tools/$P" | gzip -dc)

# /api/github: first hit caches (MISS), second serves from disk (HIT)
G=/api/github/repos/jqlang/jq/releases/jq-1.8.2
curl -s -o /dev/null -w '%header{x-cache}\n' "http://localhost:8080$G"   # MISS
curl -s -o /dev/null -w '%header{x-cache}\n' "http://localhost:8080$G"   # HIT
ls cache/                                                                # <sha>.body/.meta

# end-to-end (config must be trusted first!). python exercises /tools/*.gz,
# and a big tool set exercises the /api/github mirror under GitHub's rate limit.
mise trust ./mise.toml
mise use -v lazygit python@3.12   # expect no "Connection refused" / 404 aborts
```

The request log (stdlib middleware) prints every hit with method, path, status, and — for
the proxy — the cache disposition, so `docker compose logs -f mise-versions` shows `/data`
and `/tools` returning 200 and `/api/github` lines tagged `[HIT]`/`[MISS]`/`[STALE]`.

## Gotchas

- **Trust the config first.** Any project using `[settings.url_replacements]` must be
  `mise trust`-ed, or mise errors out before it ever contacts this server.
- mise **caches** version lists, so after one successful fetch you may not see it re-hit
  `/data/` on subsequent runs — that's expected, not a failure. Testing in a fresh
  `docker run --rm` mise container (no cache) forces the requests every time.
- **`core:python` is unforgiving:** a 404 on `/tools/<name>.gz` aborts the install
  outright. If you add a new arch/platform, make sure the matching `docs/<name>.toml`
  exists in your checkout.
- **Residual `api.github.com` calls.** mise still makes a few `releases?per_page=100`
  listing requests to GitHub *directly* (not through this host), so under a hammered rate
  limit you'll see `WARN … 403 Forbidden` lines. They're non-fatal now — the install
  completes from the mirrored release data. To silence them entirely, pass a `GITHUB_TOKEN`
  into the mise container (raises the limit to 5000/hr).
