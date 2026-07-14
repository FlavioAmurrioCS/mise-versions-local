#!/usr/bin/env bash
# Warm the /api/github disk cache by installing a list of tools against a local
# instance of this server. mise resolves each tool through the server, whose
# proxy caches the upstream /api/github responses into CACHE_DIR as
# <sha256>.meta/.body pairs. Runnable locally and from the warm-cache workflow.
#
# TOOLS_FILE format (one entry per line; blank lines and # comments ignored):
#   name             -> latest $VERSIONS versions       (e.g. `jq`)
#   name@version     -> that exact version              (e.g. `jq@1.8.2`)
#   name COUNT       -> latest COUNT versions           (e.g. `jq 10`)
# List a tool on several lines to warm several explicit versions.
#
# Env:
#   DOCS_DIR     (required) a mise-versions docs/ directory to serve /data + /tools from
#   CACHE_DIR    where the proxy writes its cache (default: <repo>/cache)
#   TOOLS_FILE   the tool list                          (default: <repo>/tools.txt)
#   VERSIONS     latest-N versions to warm per bare tool (default: 1)
#   ADDR         address the server listens on          (default: 127.0.0.1:8080)
#   GITHUB_TOKEN optional; lifts mise's direct api.github.com calls to 5000/hr
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
: "${DOCS_DIR:?set DOCS_DIR to a mise-versions docs/ directory}"
cache_dir="${CACHE_DIR:-$repo_root/cache}"
tools_file="${TOOLS_FILE:-$repo_root/tools.txt}"
addr="${ADDR:-127.0.0.1:8080}"

for cmd in go mise curl; do
	command -v "$cmd" >/dev/null || { echo "error: '$cmd' not found on PATH" >&2; exit 1; }
done
[ -d "$DOCS_DIR" ] || { echo "error: DOCS_DIR '$DOCS_DIR' is not a directory" >&2; exit 1; }
[ -f "$tools_file" ] || { echo "error: TOOLS_FILE '$tools_file' not found" >&2; exit 1; }

grep -qvE '^[[:space:]]*(#|$)' "$tools_file" || { echo "error: no tools listed in $tools_file" >&2; exit 1; }

mkdir -p "$cache_dir"
server_bin="$(mktemp -d)/mvl"
( cd "$repo_root" && go build -o "$server_bin" . )

echo ">> starting server on $addr (docs=$DOCS_DIR cache=$cache_dir)"
DOCS_DIR="$DOCS_DIR" CACHE_DIR="$cache_dir" ADDR="$addr" "$server_bin" &
server_pid=$!
cleanup() { kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; }
trap cleanup EXIT

for _ in $(seq 1 40); do
	curl -sf -o /dev/null "http://$addr/health" && break
	sleep 0.5
done
curl -sf -o /dev/null "http://$addr/health" || { echo "error: server did not become healthy" >&2; exit 1; }

# Isolate mise from ambient config so we warm exactly $tools and nothing else:
# scratch data/cache/config dirs (no host state, no global mise.toml) plus a
# scratch working dir (no repo-local mise.toml). url_replacements via env needs
# no `mise trust`, and `mise install <tools>` needs no config file.
MISE_DATA_DIR="$(mktemp -d)"; export MISE_DATA_DIR
MISE_CACHE_DIR="$(mktemp -d)"; export MISE_CACHE_DIR
MISE_CONFIG_DIR="$(mktemp -d)"; export MISE_CONFIG_DIR
# shellcheck disable=SC2089  # JSON literal; the quotes are meant literally
export MISE_URL_REPLACEMENTS="{ \"https://mise-versions.jdx.dev\": \"http://$addr\" }"
workdir="$(mktemp -d)"

# Expand the tool list into concrete tool@version specs. "latest N" entries are
# resolved via `mise ls-remote` (which queries this server), taking the newest N.
default_versions="${VERSIONS:-1}"
install_list=""
while IFS= read -r line || [ -n "$line" ]; do
	line="${line%%#*}" # strip trailing comments
	# shellcheck disable=SC2086  # split line into spec + optional count
	set -- $line
	spec="${1:-}"
	count="${2:-$default_versions}"
	[ -n "$spec" ] || continue
	case "$spec" in
	*@*) # pinned exact version — take as-is
		install_list="$install_list $spec" ;;
	*)
		case "$count" in '' | *[!0-9]*) count=1 ;; esac
		if [ "$count" -le 1 ]; then
			install_list="$install_list $spec"
		else
			vers="$(mise ls-remote "$spec" 2>/dev/null | tail -n "$count")"
			if [ -z "$vers" ]; then
				echo ">> WARN: no remote versions for '$spec'; warming latest only" >&2
				install_list="$install_list $spec"
			else
				for v in $vers; do install_list="$install_list $spec@$v"; done
			fi
		fi ;;
	esac
done <"$tools_file"
install_list="${install_list# }"

echo ">> installing: $install_list"
# shellcheck disable=SC2086  # intentional word-splitting of the spec list
( cd "$workdir" && mise install -v $install_list ) || echo ">> WARN: some tools failed to install (continuing)"

entries="$(find "$cache_dir" -name '*.meta' | wc -l | tr -d ' ')"
echo ">> done: $entries cached /api/github entries in $cache_dir"
