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
- `DOCS_DIR` — folder holding the `<tool>.toml` version lists (default `docs`; the repo-root
  loop uses `./data`).
- `ADDR` — listen address (default `:8080`, must match the url_replacements target).
- `CACHE_DIR` — tree root the `/api/github` proxy records into (default `cache`; the repo-root
  loop uses `.`).
- `CACHE_TTL` — freshness window before revalidating a cached entry (default `1h`).
  A per-response `Cache-Control: max-age` from the upstream overrides this.
- `UPSTREAM` — host to mirror `/api/github/*` from (default `https://mise-versions.jdx.dev`).

## The /api/github mirror

`/api/github/*` is a disk-caching reverse proxy (see `mirror` in `main.go`) that records each
response at a **path that mirrors the request** — so the on-disk tree is servable as-is by a
static host (that's what makes the [GitHub Pages](#static-mirror-on-github-pages) mirror work):

- **Path-mirrored + sidecar.** A `200` is written to `CACHE_DIR/api/github/repos/.../latest`
  (the exact request path) with a `<path>.meta` sidecar holding the headers. The body file is
  byte-for-byte what the upstream sent; a static host serves it directly and ignores `.meta`.
- **Serves from disk first.** A fresh entry returns `X-Cache: HIT` with no network call.
  Freshness follows the upstream's `Cache-Control: max-age` (≈2h), falling back to `CACHE_TTL`.
- **Revalidates when stale** via `If-None-Match` / `If-Modified-Since` — `304` refreshes the
  entry in place (`REVALIDATED`), otherwise the new body replaces it (`MISS`).
- **Survives the upstream being down.** If the upstream is unreachable but a copy exists — even
  a stale one — that copy is served (`X-Cache: STALE`). Only a cold miss with no upstream `502`s.
- **Portable & git-friendly.** Entries hold no absolute paths, so you can stop the server and
  copy — or **commit** — `CACHE_DIR` and it's reused as-is.
- Non-2xx upstream responses (e.g. a `404` for an unmirrored repo) pass straight through
  uncached (`X-Cache: BYPASS`) so mise can still fall back to `api.github.com`.

The disposition tag appears in the request log, e.g.
`GET /api/github/repos/jqlang/jq/releases/jq-1.8.2 -> 200 [HIT]`.

## Static mirror on GitHub Pages

Because the `/api/github` proxy records responses at a **path that mirrors the request**, the
whole mirror is just a directory tree that a dumb static host can serve — the request path *is*
the file path. That tree lives **at the repo root on `main`**, alongside the code, and GitHub
Pages serves it from `/`:

```
<repo root>/
  data/<tool>.toml            # synced from jdx/mise-versions docs/  (served at /data/<tool>.toml)
  tools/<name>.gz             # reconstructed cpython asset lists     (served at /tools/<name>.gz)
  api/github/repos/.../latest # warmed release metadata (+ .meta)     (served at /api/github/...)
  .nojekyll                   # serve files as-is (no Jekyll build)
```

Point mise at the Pages URL — no running server, no `mise trust` (the env var needs none):

```bash
export MISE_URL_REPLACEMENTS='{ "https://mise-versions.jdx.dev": "https://<owner>.github.io/mise-versions-local" }'
mise use -v lazygit
```

_One-time setup:_ repo **Settings → Pages → Source = Deploy from a branch → `main` / (root)**.

> **Header caveat.** GitHub Pages sets its own `Content-Type`/`Content-Encoding` and can't be
> overridden — `.toml` may not be `application/toml`, and `.gz` may arrive with
> `Content-Encoding: gzip`. mise is generally tolerant, but verify with a real `mise install`
> against the Pages URL. If a MIME/encoding mismatch bites, the Go server / Docker image (which
> set correct headers) remain the guaranteed-correct way to serve the same tree, or deploy it to
> a host that honors a `_headers` file (e.g. Cloudflare Pages, which upstream itself uses).

### Growing the tree

The tree is filled by the server (for `api/github/`) plus two scripts (for the derived parts):
- **`scripts/sync-docs.sh`** — pulls the latest `jdx/mise-versions` `docs/` into `data/` (the
  source of truth for `/data/<tool>.toml`) and records the synced commit in `SYNC.json`.
- **`scripts/build-site.sh`** — reconstructs `tools/<name>.gz` from the synced `data/*.toml`.

**Local loop** — checkout, run, install, commit (`scripts/serve.sh` bakes in the right dirs):

```bash
scripts/sync-docs.sh                                  # refresh ./data
scripts/serve.sh &                                    # serve ./data + record into ./api
MISE_URL_REPLACEMENTS='{"https://mise-versions.jdx.dev":"http://localhost:8080"}' \
  mise install lazygit uv                             # records api/github/... as it resolves
git add data tools api && git commit -m "warm mirror" # the recorded files are ready to commit
```

**Automated** — `.github/workflows/warm-cache.yml` does the same every 2h: it installs the
tools in `tools.txt` on **amd64 + arm64** (the committed `api/` seeds the cache, so warming is
**incremental** — only new/stale entries hit upstream), merges the two arches, and commits the
refreshed `data/ + tools/ + api/` back to `main`. Knobs:
- **`tools.txt`** — one entry per line: `name` (latest `VERSIONS`, default 1), `name@version`
  (exact), or `name COUNT` (latest COUNT via `mise ls-remote`); repeat a tool on several lines
  for specific versions. Set a `VERSIONS` repo variable (or the manual-run input) to warm the
  latest N of every bare tool. Cap heavy toolchains per line (e.g. `rust 2`).
- `scripts/warm-cache.sh` is what the workflow runs, and is runnable locally too — it warms
  `tools.txt` against the server in an isolated mise env (scratch data/cache/config dirs, so
  only `tools.txt` is warmed, not your global config).
- Cross-arch: `/api/github/.../releases/*` is arch-independent; only `.../attestations/<digest>`
  is arch-specific, hence the matrix. Merge is a plain copy — shared entries share a path.

## Docker (self-contained mirror)

The `Dockerfile` bakes the in-repo tree (`data/ + tools/ + api/`) onto a distroless static
image, so the container is a fully-populated, offline-capable mirror — no download at runtime.
Build it after the tree is populated (via the loop above or a workflow commit):

```bash
docker build -t mise-versions-local .
docker run -p 8080:8080 mise-versions-local
```

`docker compose up --build` works too (it watches `main.go`).
Stale bundle entries revalidate against the upstream on use and fall back to the bundled
copy when offline — the same `HIT`/`STALE` behaviour described above.

> The workflow needs `contents: write` (already declared) to publish the release. To warm
> the latest N versions of the github-release tools on the schedule, set a `VERSIONS` repo
> variable (e.g. `10`); core tools stay at 1 (see `tools.txt`).

## Verify

```bash
# /data: byte-identical to source for the exact path mise requests
cmp <(curl -s http://localhost:8080/data/lazygit.toml) \
    /Users/flavio/dev/github.com/jdx/mise-versions/docs/lazygit.toml

# /tools: our gzipped list decompresses to the same bytes production serves
P=python-precompiled-aarch64-unknown-linux-gnu.gz
diff <(curl -s "http://localhost:8080/tools/$P" | gzip -dc) \
     <(curl -s "https://mise-versions.jdx.dev/tools/$P" | gzip -dc)

# /api/github: first hit records (MISS), second serves from disk (HIT)
G=/api/github/repos/jqlang/jq/releases/jq-1.8.2
curl -s -o /dev/null -w '%header{x-cache}\n' "http://localhost:8080$G"   # MISS
curl -s -o /dev/null -w '%header{x-cache}\n' "http://localhost:8080$G"   # HIT
cat "$CACHE_DIR$G"                                                       # the exact request path

# end-to-end via the env var (no `mise trust` needed). python exercises /tools/*.gz,
# and a big tool set exercises the /api/github mirror under GitHub's rate limit.
MISE_URL_REPLACEMENTS='{"https://mise-versions.jdx.dev":"http://localhost:8080"}' \
  mise use -v lazygit python@3.12   # expect no "Connection refused" / 404 aborts
```

The request log (stdlib middleware) prints every hit with method, path, status, and — for
the proxy — the cache disposition, so `docker compose logs -f mise-versions` shows `/data`
and `/tools` returning 200 and `/api/github` lines tagged `[HIT]`/`[MISS]`/`[STALE]`.

## Gotchas

- **Trust the config first.** A project using `[settings.url_replacements]` must be
  `mise trust`-ed, or mise errors out before it ever contacts this server. The
  `MISE_URL_REPLACEMENTS` **env var** (used by the scripts and for Pages) needs no trust.
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
