#!/usr/bin/env bash
tmp_dir=$(mktemp -d)

git clone --depth=1 --single-branch git@github.com:jdx/mise-versions.git "$tmp_dir"
cp "$tmp_dir/docs"/* data/
rm data/index.html
rm -rf "$tmp_dir"
