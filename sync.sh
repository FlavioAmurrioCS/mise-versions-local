#!/usr/bin/env bash
tmp_dir=$(mktemp -d)

git clone --depth=1 --single-branch git@github.com:jdx/mise-versions.git "$tmp_dir"
mkdir -p mise-versions.jdx.dev/data/
cp "$tmp_dir/docs"/* mise-versions.jdx.dev/data/
rm mise-versions.jdx.dev/data/index.html
rm -rf "$tmp_dir"
