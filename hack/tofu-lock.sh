#!/bin/sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
version=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$root/internal/tofu/binary.go")
tofu=${TOFU:-$HOME/.kubit/bin/tofu-$version}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
sed 's/backend "kubernetes" {}//' "$root/internal/tofu/templates/platform/versions.tf" > "$work/versions.tf"
cd "$work"
"$tofu" providers lock -platform=darwin_arm64 -platform=darwin_amd64 -platform=linux_amd64 -platform=linux_arm64
cp .terraform.lock.hcl "$root/internal/tofu/templates/platform/.terraform.lock.hcl"
