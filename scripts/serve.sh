#!/usr/bin/env bash
# Start the mirror server against the repo-root tree so the local warm loop is
# foolproof: it serves /data + /tools from ./data and records /api/github into
# the repo root (./api/github/...), i.e. exactly the files GitHub Pages serves.
#
# Loop:
#   scripts/sync-docs.sh   # populate ./data from jdx/mise-versions (once/occasionally)
#   scripts/serve.sh &     # this server
#   MISE_URL_REPLACEMENTS='{"https://mise-versions.jdx.dev":"http://localhost:8080"}' \
#     mise install <tools> # records api/github/... as you install
#   git add data tools api && git commit   # the recorded files land ready to commit
#
# Env: ADDR (default :8080), plus the usual CACHE_TTL / UPSTREAM. Needs `go` on PATH
# (or run via `mise x go@1.26.4 -- scripts/serve.sh`).
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
command -v go >/dev/null || { echo "error: 'go' not found on PATH (try: mise x go@1.26.4 -- scripts/serve.sh)" >&2; exit 1; }
[ -d data ] || { echo "error: ./data missing — run scripts/sync-docs.sh first" >&2; exit 1; }

exec env DOCS_DIR="$repo_root/data" CACHE_DIR="$repo_root" go run .
