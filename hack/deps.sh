#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

out=${DEPS_OUT:-$root/out}
mkdir -p "$out"
changes="$out/changes.tsv"
review="$out/review.tsv"
: >"$changes"
: >"$review"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

gh_api() { curl -fsSL ${GITHUB_TOKEN:+-H "Authorization: Bearer $GITHUB_TOKEN"} -H 'Accept: application/vnd.github+json' "https://api.github.com/$1"; }

strip_v() { echo "${1#v}"; }
major() { strip_v "$1" | cut -d. -f1; }
minor() { strip_v "$1" | cut -d. -f2; }
newer() { [ "$(strip_v "$1")" != "$(strip_v "$2")" ] && [ "$(printf '%s\n%s\n' "$(strip_v "$1")" "$(strip_v "$2")" | sort -V | tail -1)" = "$(strip_v "$1")" ]; }
stable() { [[ $(strip_v "$1") =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; }

kind() {
	if [ "$(major "$1")" != "$(major "$2")" ]; then echo major
	elif [ "$(minor "$1")" != "$(minor "$2")" ]; then echo minor
	else echo patch; fi
}

blocked() { [ -f hack/deps.blocked ] && awk -v c="$1" -v v="$(strip_v "$2")" '$1 == c { sub(/^v/, "", $2); if ($2 == v) f = 1 } END { exit !f }' hack/deps.blocked; }

applied() { printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$(kind "$2" "$3")" >>"$changes"; echo "  $1 $2 → $3"; }
report() { printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >>"$review"; echo "  review: $1 $2 → $3 ($4)" >&2; }

newest_stable() { grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -1; }

pick() {
	local name=$1 cur=$2 cand best="" top=""
	shift 2
	for cand in "$@"; do
		stable "$cand" && newer "$cand" "$cur" || continue
		blocked "$name" "$cand" && continue
		if [ "$(kind "$cur" "$cand")" = major ]; then
			if [ -z "$top" ] || newer "$cand" "$top"; then top=$cand; fi
		elif [ -z "$best" ] || newer "$cand" "$best"; then
			best=$cand
		fi
	done
	if [ -n "$top" ]; then report "$name" "$cur" "$top" major; fi
	echo "$best"
}

go_toolchain() {
	echo "Go toolchain"
	local cur best
	cur=$(go mod edit -json | jq -r .Go)
	all=($(curl -fsSL 'https://go.dev/dl/?mode=json' | jq -r '.[] | select(.stable) | .version | ltrimstr("go")'))
	best=""
	for v in "${all[@]}"; do
		stable "$v" && newer "$v" "$cur" || continue
		if [ "$(kind "$cur" "$v")" = patch ]; then best=$v
		else report go "$cur" "$v" "$(kind "$cur" "$v")"; fi
	done
	if [ -n "$best" ] && ! blocked go "$best"; then
		go mod edit -go="$best"
		applied go "$cur" "$best"
	fi
}

machinery() {
	echo "Talos machinery"
	local mod=github.com/siderolabs/talos/pkg/machinery cur best v k8s
	cur=$(go list -m -f '{{.Version}}' "$mod")
	best=""
	for v in $(go list -m -versions "$mod" | cut -d' ' -f2-); do
		stable "$v" && newer "$v" "$cur" || continue
		blocked "$mod" "$v" && continue
		case $(kind "$cur" "$v") in
		major) report "$mod" "$cur" "$v" major ;;
		minor) [ "$(strip_v "$v" | cut -d. -f3)" -ge 1 ] && best=$v ;;
		patch) best=$v ;;
		esac
	done
	[ -z "$best" ] && return 0
	go get "$mod@$best"
	k8s=$(sed -n 's/.*DefaultKubernetesVersion *= *"\([0-9.]*\)".*/\1/p' "$(go env GOMODCACHE)/$mod@$best/constants/constants.go")
	for m in k8s.io/api k8s.io/apimachinery k8s.io/client-go k8s.io/kubectl k8s.io/metrics; do
		local was want
		was=$(go list -m -f '{{.Version}}' "$m")
		want=$(go list -m -versions "$m" | tr ' ' '\n' | grep -E "^v0\.$(minor "$k8s")\.[0-9]+$" | sort -V | tail -1)
		if [ -n "$want" ] && [ "$want" != "$was" ]; then
			go get "$m@$want"
		fi
	done
	sed -i.bak -E "s/Talos machinery v[0-9.]+ \(Kubernetes [0-9.]+ default\)/Talos machinery $best (Kubernetes $k8s default)/" docs/development.md
	sed -i.bak -E "s/Talos%20Linux-v[0-9]+\.[0-9]+-/Talos%20Linux-v$(major "$best").$(minor "$best")-/; s/Kubernetes-v[0-9]+\.[0-9]+-/Kubernetes-v$(major "$k8s").$(minor "$k8s")-/" README.md
	rm -f docs/development.md.bak README.md.bak
}

go_modules() {
	echo "Go modules"
	local path cur upd
	while IFS=$'\t' read -r path cur upd; do
		case $path in github.com/siderolabs/talos/* | k8s.io/*) continue ;; esac
		if [[ $cur =~ -[0-9]{14}-[0-9a-f]{12}$ ]]; then
			if [ -n "$upd" ]; then report "$path" "$cur" "$upd" pseudo-version; fi
			continue
		fi
		[ -z "$upd" ] && continue
		if ! stable "$cur" || ! stable "$upd"; then
			report "$path" "$cur" "$upd" prerelease
			continue
		fi
		blocked "$path" "$upd" && continue
		go get "$path@$upd"
	done < <(go list -m -u -json all | jq -r 'select(.Main != true and .Indirect != true) | [.Path, .Version, (.Update.Version // "")] | @tsv')
	go mod tidy
	go mod verify
}

npm_packages() {
	echo "npm"
	local outdated name cur want latest
	(cd web && npm ci --no-audit --no-fund >/dev/null)
	outdated=$(cd web && npm outdated --json || true)
	[ -z "$outdated" ] && return 0
	while IFS=$'\t' read -r name cur want latest; do
		if [ "$want" != "$cur" ] && newer "$want" "$cur" && ! blocked "npm:$name" "$want"; then
			applied "npm:$name" "$cur" "$want"
		fi
		if [ "$latest" != "$want" ] && newer "$latest" "$want"; then
			report "npm:$name" "$want" "$latest" "$(kind "$want" "$latest")"
		fi
	done < <(echo "$outdated" | jq -r 'to_entries[] | [.key, .value.current, .value.wanted, .value.latest] | @tsv')
	(cd web && npm update --save && npm ci && npm audit signatures)
}

opentofu() {
	echo "OpenTofu"
	local cur best tag sums platform
	cur=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/tofu/binary.go)
	tags=($(gh_api 'repos/opentofu/opentofu/releases?per_page=50' | jq -r '.[] | select(.prerelease | not) | .tag_name'))
	best=$(pick opentofu "$cur" "${tags[@]}")
	if [ -n "$best" ]; then
		best=$(strip_v "$best")
		local base="https://github.com/opentofu/opentofu/releases/download/v$best/tofu_${best}_SHA256SUMS"
		sums="$work/tofu_SHA256SUMS"
		curl -fsSL "$base" -o "$sums"
		curl -fsSL "$base.sig" -o "$sums.sig"
		curl -fsSL "$base.pem" -o "$sums.pem"
		if ! cosign verify-blob --certificate "$sums.pem" --signature "$sums.sig" \
			--certificate-identity-regexp '^https://github\.com/opentofu/opentofu/\.github/workflows/release\.yml@refs/heads/v[0-9]+\.[0-9]+$' \
			--certificate-oidc-issuer https://token.actions.githubusercontent.com "$sums"; then
			report opentofu "$cur" "$best" "signature does not verify"
			best=""
		fi
	fi
	if [ -n "$best" ]; then
		for platform in darwin_arm64 darwin_amd64 linux_amd64 linux_arm64; do
			local sum
			sum=$(awk -v f="tofu_${best}_${platform}.zip" '$2 == f {print $1}' "$sums")
			[ -n "$sum" ] || { echo "no checksum for $platform" >&2; exit 1; }
			sed -i.bak -E "s/(\"$platform\": +\")[0-9a-f]{64}\"/\1$sum\"/" internal/tofu/binary.go
		done
		sed -i.bak -E "s/^const Version = \"[0-9.]+\"$/const Version = \"$best\"/" internal/tofu/binary.go
		rm -f internal/tofu/binary.go.bak
		applied opentofu "$cur" "$best"
		cur=$best
	fi
	local host
	case "$(uname -s)_$(uname -m)" in
	Linux_x86_64) host=linux_amd64 ;; Linux_aarch64) host=linux_arm64 ;;
	Darwin_arm64) host=darwin_arm64 ;; Darwin_x86_64) host=darwin_amd64 ;;
	esac
	local zip="$work/tofu.zip" want
	curl -fsSL "https://github.com/opentofu/opentofu/releases/download/v$cur/tofu_${cur}_${host}.zip" -o "$zip"
	want=$(sed -n "s/.*\"$host\": *\"\([0-9a-f]*\)\".*/\1/p" internal/tofu/binary.go)
	echo "$want  $zip" | shasum -a 256 -c - >/dev/null
	unzip -oq "$zip" tofu -d "$work"
	providers "$work/tofu"
}

providers() {
	echo "Providers"
	local lock=internal/tofu/templates/platform/.terraform.lock.hcl before after p was now
	before=$(awk '/^provider/{p=$2} /version *=/{print p, $3}' "$lock" | tr -d '"')
	TOFU=$1 hack/tofu-lock.sh
	after=$(awk '/^provider/{p=$2} /version *=/{print p, $3}' "$lock" | tr -d '"')
	while read -r p now; do
		was=$(echo "$before" | awk -v p="$p" '$1 == p {print $2}')
		if [ "$was" != "$now" ]; then applied "provider:${p#registry.opentofu.org/}" "$was" "$now"; fi
		local latest
		latest=$(curl -fsSL "https://registry.opentofu.org/v1/providers/${p#registry.opentofu.org/}/versions" | jq -r '.versions[].version' | newest_stable)
		if [ -n "$latest" ] && [ "$(kind "$now" "$latest")" = major ]; then report "provider:${p#registry.opentofu.org/}" "$now" "$latest" major; fi
	done <<<"$after"
}

charts() {
	echo "Helm charts"
	local cat=internal/tofu/charts.json name repo chart last idx v
	[ -f "$cat" ] || { echo "  $cat missing" >&2; exit 1; }
	local k8s
	k8s=$(sed -n 's/.*DefaultKubernetesVersion *= *"\([0-9.]*\)".*/\1/p' "$(go env GOMODCACHE)/github.com/siderolabs/talos/pkg/machinery@$(go list -m -f '{{.Version}}' github.com/siderolabs/talos/pkg/machinery)/constants/constants.go")
	for name in $(jq -r 'keys[]' "$cat"); do
		repo=$(jq -r --arg n "$name" '.[$n].repo' "$cat")
		chart=$(jq -r --arg n "$name" '.[$n].chart' "$cat")
		last=$(jq -r --arg n "$name" '.[$n].steps[-1].version' "$cat")
		idx="$work/$name-index.yaml"
		curl -fsSL "${repo%/}/index.yaml" -o "$idx"
		versions=($(yq -r ".entries[\"$chart\"][].version" "$idx" | grep -E '^v?[0-9]+\.[0-9]+\.[0-9]+$' | sort -V))
		local top=""
		for v in "${versions[@]}"; do
			newer "$v" "$last" || continue
			blocked "chart:$name" "$v" && continue
			[ "$(kind "$last" "$v")" = major ] && { top=$v; continue; }
			local later
			later=$(printf '%s\n' "${versions[@]}" | grep -E "^v?$(major "$v")\.$(minor "$v")\.[0-9]+$" | sort -V | tail -1)
			[ "$v" = "$later" ] || continue
			local digest kube tgz
			digest=$(yq -r ".entries[\"$chart\"][] | select(.version == \"$v\") | .digest" "$idx")
			kube=$(yq -r ".entries[\"$chart\"][] | select(.version == \"$v\") | .kubeVersion // \"\"" "$idx")
			tgz="$work/$name-$v.tgz"
			local url
			url=$(yq -r ".entries[\"$chart\"][] | select(.version == \"$v\") | .urls[0]" "$idx")
			[[ $url == http* ]] || url="${repo%/}/$url"
			curl -fsSL "$url" -o "$tgz"
			if [ "$(shasum -a 256 "$tgz" | awk '{print $1}')" != "$digest" ]; then
				report "chart:$name" "$last" "$v" "digest mismatch"
				continue
			fi
			if ! helm template probe "$tgz" --kube-version "$k8s" >/dev/null; then
				report "chart:$name" "$last" "$v" "does not render on Kubernetes $k8s"
				continue
			fi
			local step prev
			step=$(jq -n --arg v "$v" --arg d "$digest" --arg k "$kube" '{version: $v, digest: $d, kubeVersion: $k}')
			if [ "$name" = traefik ]; then
				local app gw
				app=$(yq -r ".entries[\"$chart\"][] | select(.version == \"$v\") | .appVersion" "$idx")
				gw=$(curl -fsSL "https://raw.githubusercontent.com/traefik/traefik/$app/go.mod" | awk '$1 == "sigs.k8s.io/gateway-api" {print $2}')
				if [ -z "$gw" ]; then
					report "chart:$name" "$last" "$v" "Gateway API version unknown"
					continue
				fi
				step=$(jq --arg g "$gw" '. + {gatewayAPI: $g}' <<<"$step")
			fi
			prev=$(jq -r --arg n "$name" '.[$n].steps[-1].version' "$cat")
			if [ "$(major "$prev").$(minor "$prev")" = "$(major "$v").$(minor "$v")" ]; then
				jq --arg n "$name" --argjson s "$step" '.[$n].steps[-1] = $s' "$cat" >"$cat.new"
			else
				jq --arg n "$name" --argjson s "$step" '.[$n].steps += [$s]' "$cat" >"$cat.new"
			fi
			mv "$cat.new" "$cat"
			applied "chart:$name" "$prev" "$v"
		done
		if [ -n "$top" ]; then report "chart:$name" "$last" "$top" major; fi
	done
}

gateway_api() {
	echo "Gateway API"
	local dir=internal/tofu/templates/platform/charts/gateway-api cur want url digest file
	cur=$(yq -r .appVersion "$dir/Chart.yaml")
	want=$(jq -r '.traefik.steps[-1].gatewayAPI // empty' internal/tofu/charts.json)
	[ -n "$want" ] && [ "$want" != "$cur" ] || return 0
	blocked gateway-api "$want" && return 0
	url="https://github.com/kubernetes-sigs/gateway-api/releases/download/$want/standard-install.yaml"
	digest=$(gh_api "repos/kubernetes-sigs/gateway-api/releases/tags/$want" | jq -r '.assets[] | select(.name == "standard-install.yaml") | .digest')
	file="$work/standard-install.yaml"
	curl -fsSL "$url" -o "$file"
	if [ "sha256:$(shasum -a 256 "$file" | awk '{print $1}')" != "$digest" ]; then
		report gateway-api "$cur" "$want" "digest mismatch"
		return 0
	fi
	awk '$0 == "---" { crd = 0 } $0 == "kind: CustomResourceDefinition" { crd = 1 } crd && prev == "metadata:" && $0 == "  annotations:" { print; print "    helm.sh/resource-policy: keep"; prev = $0; next } { print; prev = $0 }' "$file" >"$dir/templates/crds.yaml"
	[ "$(grep -c '^kind: CustomResourceDefinition' "$dir/templates/crds.yaml")" = "$(grep -c 'helm.sh/resource-policy: keep' "$dir/templates/crds.yaml")" ] || { echo "gateway-api $want: CRD layout changed" >&2; exit 1; }
	yq -i ".version = \"$(strip_v "$want")\" | .appVersion = \"$want\"" "$dir/Chart.yaml"
	sed -i.bak "s/Gateway API CRDs $cur/Gateway API CRDs $want/" docs/operations.md && rm -f docs/operations.md.bak
	applied gateway-api "$cur" "$want"
}

buildkit() {
	echo "buildkit"
	local file=internal/tofu/templates/platform/builds.tf cur best
	cur=$(sed -n 's#.*"moby/buildkit:\(v[0-9.]*\)".*#\1#p' "$file")
	tags=($(gh_api 'repos/moby/buildkit/releases?per_page=50' | jq -r '.[] | select(.prerelease | not) | .tag_name'))
	best=$(pick buildkit "$cur" "${tags[@]}")
	[ -z "$best" ] && return 0
	docker buildx imagetools inspect "moby/buildkit:$best" >/dev/null
	sed -i.bak "s#moby/buildkit:$cur#moby/buildkit:$best#" "$file" && rm -f "$file.bak"
	applied buildkit "$cur" "$best"
}

report_only() {
	echo "Report only"
	local cur latest
	cur=$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/sops/sops.go)
	latest=$(gh_api repos/getsops/sops/releases/latest | jq -r .tag_name)
	if newer "$latest" "$cur"; then report sops-format "$cur" "$latest" "$(kind "$cur" "$latest")"; fi
	local backup
	backup=$(sed -n 's/^const TalosBackupVersion = "\(.*\)"$/\1/p' internal/tofu/charts.go)
	if [ -n "$backup" ]; then
		latest=$(gh_api 'repos/siderolabs/talos-backup/releases?per_page=20' | jq -r '.[0].tag_name')
		if [ "$latest" != "$backup" ]; then report talos-backup "$backup" "$latest" image; fi
	fi
	local line action pin tag
	while read -r line; do
		action=${line%@*}
		pin=$(echo "$line" | sed -n 's/.*# \(v[0-9.]*\).*/\1/p')
		tag=$(gh_api "repos/$action/releases/latest" | jq -r .tag_name)
		[ -n "$pin" ] && stable "$tag" || continue
		if [[ $pin != *.* ]]; then
			if [ "$(major "$tag")" -gt "$(major "$pin")" ]; then report "action:$action" "$pin" "$tag" major; fi
		elif newer "$tag" "$pin"; then
			report "action:$action" "$pin" "$tag" "$(kind "$pin" "$tag")"
		fi
	done < <(grep -ho 'uses: [^ ]*@[0-9a-f]\{40\} # v[0-9.]*' .github/workflows/*.yml | sed 's/^uses: //' | sort -u)
}

direct_modules() { go list -m -f '{{if not (or .Main .Indirect)}}{{.Path}} {{.Version}}{{end}}' all; }

go_toolchain
before=$(direct_modules)
machinery
go_modules
while read -r path now; do
	was=$(awk -v p="$path" '$1 == p {print $2}' <<<"$before")
	if [ -n "$was" ] && [ "$was" != "$now" ]; then applied "$path" "$was" "$now"; fi
done < <(direct_modules)
npm_packages
opentofu
charts
gateway_api
buildkit
report_only

sort -u -o "$changes" "$changes"
sort -u -o "$review" "$review"
echo "applied: $(wc -l <"$changes" | tr -d ' '), review: $(wc -l <"$review" | tr -d ' ')"
