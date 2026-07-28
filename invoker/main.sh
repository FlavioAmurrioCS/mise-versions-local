#!/usr/bin/env bash
curl https://mise.run | sh

export PATH="$HOME/.local/bin:$PATH"

~/.local/bin/mise --version || exit 1

number_of_versions=5
export MISE_MINIMUM_RELEASE_AGE=0h

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

tools=(
    docker-cli@latest
    python@latest
    python@latest
    python@latest
    python@latest
    python@latest
    java@latest
    java@latest
    java@latest
    java@latest
    node@latest
    node@latest
    fzf@latest
    neovim@latest
    uv@latest
    pipx:pipenv@latest
    pre-commit@latest
    pipx:pre-commit@latest
    go@latest
    docker-compose@latest
    github:docker/buildx@latest
    github:docker/docker-credential-helpers@latest
    go:github.com/dolmen-go/docker-list-context@latest
    dive@latest
)

for tool in "${tools[@]}"; do
    mise install "${tool}"
done
