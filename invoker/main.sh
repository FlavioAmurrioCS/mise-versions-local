#!/usr/bin/env bash
curl https://mise.run | sh

export PATH="$HOME/.local/bin:$PATH"

~/.local/bin/mise --version || exit 1

number_of_versions=3

tools=(
    docker-cli
    python@3.10
    python@3.11
    python@3.12
    python@3.13
    python@3.14
    java@11
    java@17
    java@21
    java@25
    node@22
    node@24
    fzf
    neovim
    uv
    pipx:pipenv
    pre-commit
    pipx:pre-commit
    go
    docker-compose
    github:docker/buildx
    github:docker/docker-credential-helpers
    go:github.com/dolmen-go/docker-list-context
    dive
)

mise use -g uv python@3.12 go
# while read -r tool; do
#     mise ls-remote "${tool}"
# done < <(mise registry | awk '{print $1}' | head -n 10)
for tool in "${tools[@]}"; do
    ltool=$(echo "${tool}" | cut -d '@' -f 1)
    while read -r version; do
        mise install "${ltool}@${version}"
    done < <(mise ls-remote "${tool}" | tail -n "${number_of_versions}")
done
