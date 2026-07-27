#!/usr/bin/env bash
curl https://mise.run | sh

export PATH="$HOME/.local/bin:$PATH"

~/.local/bin/mise --version || exit 1

number_of_versions=1

tools=(
    # docker-cli
    # python@3.13
    fzf
)


# while read -r tool; do
#     mise ls-remote "${tool}"
# done < <(mise registry | awk '{print $1}' | head -n 10)
for tool in "${tools[@]}"; do
    ltool=$(echo "${tool}" | cut -d '@' -f 1)
    while read -r version; do
        mise install -v "${ltool}@${version}"
    done < <(mise ls-remote -v "${tool}" | tail -n "${number_of_versions}")
done
