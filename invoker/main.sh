#!/usr/bin/env bash
curl https://mise.run | sh

export PATH="$HOME/.local/bin:$PATH"

~/.local/bin/mise --version || exit 1

number_of_versions=5
export MISE_MINIMUM_RELEASE_AGE=0h
export MISE_JAVA_SHORTHAND_VENDOR=corretto

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
    maven
    gradle
)
arch=$(uname -m)
rm install_"${arch}"_*.txt 2>/dev/null || true

mise use -g -v uv python@3.12 go
# while read -r tool; do
#     mise ls-remote "${tool}"
# done < <(mise registry | awk '{print $1}' | head -n 10)
for tool in "${tools[@]}"; do
    ltool=$(echo "${tool}" | cut -d '@' -f 1)
    count=0
    while read -r version; do
        echo "${ltool}@${version}" >> "install_${arch}_${count}.txt"
        count=$((count + 1))
    done < <(mise ls-remote "${tool}" | tail -n "${number_of_versions}")
done

for i in $(seq 0 $((number_of_versions - 1))); do
    cat "install_${arch}_${i}.txt" | sort -u | xargs mise install -v
done

tools=(
    dive@latest
    docker-cli@latest
    docker-compose@latest
    fzf@latest
    github:docker/buildx@latest
    github:docker/docker-credential-helpers@latest
    go:github.com/dolmen-go/docker-list-context@latest
    go@latest
    java@latest
    neovim@latest
    node@latest
    pipx:pipenv@latest
    pipx:pre-commit@latest
    pre-commit@latest
    python@latest
    uv@latest
    maven@latest
    gradle@latest
)


mise install -v "${tools[@]}"
