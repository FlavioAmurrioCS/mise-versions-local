#!/usr/bin/env bash
# Reconstruct tools/<name>.gz in the mirror tree from the synced data/<name>.toml.
# mise (core:python) fetches /tools/<name>.gz — a gzipped, newline-separated list
# of the tool's versions (each [versions] key in the toml). Upstream never serves
# this file (it 404s), so a static host must ship a pre-built copy. This mirrors
# toolsGzHandler in main.go; mise gunzips it, so only the decompressed content
# matters (gzip -n keeps the bytes deterministic for clean git diffs). Run after
# sync-docs.sh.
#
# Env:
#   TREE_DIR   (required) mirror tree root (reads data/, writes tools/)
#   GZ_TOOLS   space-separated tool names to generate (default: python)
set -euo pipefail

: "${TREE_DIR:?set TREE_DIR to the mirror tree root}"
gz_tools="${GZ_TOOLS:-python}"

command -v gzip >/dev/null || { echo "error: 'gzip' not found on PATH" >&2; exit 1; }

data_dir="$TREE_DIR/data"
tools_dir="$TREE_DIR/tools"
mkdir -p "$tools_dir"

made=0
for name in $gz_tools; do
	toml="$data_dir/$name.toml"
	if [ ! -f "$toml" ]; then
		echo ">> WARN: $toml not found; skipping $name.gz" >&2
		continue
	fi
	# Print each [versions] key (the text between the first pair of quotes), one
	# per line, restricted to the [versions] table — then gzip deterministically.
	awk '
		/^\[/ { inv = ($0 == "[versions]"); next }
		inv && /^"/ { s = $0; sub(/^"/, "", s); sub(/".*/, "", s); print s }
	' "$toml" | gzip -n >"$tools_dir/$name.gz"
	made=$((made + 1))
done
echo ">> generated $made tools/*.gz in $tools_dir"
