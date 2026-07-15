#!/usr/bin/env bash
# Sync data/<tool>.toml from the source of truth — the docs/ folder of
# jdx/mise-versions — into a mirror tree. /data/<tool>.toml is served as a
# byte-for-byte copy of that upstream docs/ file (README key finding), so keeping
# them in lockstep just means re-copying the latest docs. Runnable locally and
# from the warm-cache workflow (which folds its inline download into this).
#
# Env:
#   TREE_DIR   (required) mirror tree root; tomls land in $TREE_DIR/data
#   REPO       source repo            (default: jdx/mise-versions)
#   REF        branch/tag/sha to sync (default: main)
set -euo pipefail

: "${TREE_DIR:?set TREE_DIR to the mirror tree root}"
repo="${REPO:-jdx/mise-versions}"
ref="${REF:-main}"

for cmd in curl tar; do
	command -v "$cmd" >/dev/null || { echo "error: '$cmd' not found on PATH" >&2; exit 1; }
done

# Resolve the commit sha for provenance (best-effort; not required to sync).
sha=""
if command -v gh >/dev/null 2>&1; then
	sha="$(gh api "repos/$repo/commits/$ref" --jq .sha 2>/dev/null || true)"
fi
if [ -z "$sha" ] && command -v jq >/dev/null 2>&1; then
	sha="$(curl -fsSL "https://api.github.com/repos/$repo/commits/$ref" 2>/dev/null | jq -r '.sha // empty' || true)"
fi
download_ref="${sha:-$ref}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo ">> fetching $repo docs @ $download_ref"
curl -fsSL "https://github.com/$repo/archive/$download_ref.tar.gz" -o "$tmp/src.tar.gz"
tar -xzf "$tmp/src.tar.gz" -C "$tmp"

# sed -n 1p (not head) reads to EOF, so find never gets SIGPIPE under pipefail.
src_docs="$(find "$tmp" -maxdepth 3 -type d -name docs | sed -n 1p)"
[ -n "$src_docs" ] || { echo "error: no docs/ dir in $repo archive" >&2; exit 1; }

data_dir="$TREE_DIR/data"
# Redraw data/ from scratch so tools removed upstream disappear here too; git
# still commits only the tomls that actually changed.
rm -rf "$data_dir"
mkdir -p "$data_dir"
cp -a "$src_docs/." "$data_dir/"

count="$(find "$data_dir" -name '*.toml' | wc -l | tr -d ' ')"
mkdir -p "$TREE_DIR"
cat >"$TREE_DIR/SYNC.json" <<EOF
{
  "source_repo": "$repo",
  "ref": "$ref",
  "commit": "${sha:-unknown}",
  "synced_at": "$(date -u +%FT%TZ)",
  "tool_count": $count
}
EOF
echo ">> synced $count tool tomls into $data_dir (commit ${sha:-unknown})"
