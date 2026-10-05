#!/usr/bin/env bash
set -euo pipefail

subnet=${1:?usage: hack/e2e.sh <subnet> [repo-dir]}
dir=${2:-$(mktemp -d)/e2e}
kubit=${KUBIT:-kubit}
log() { printf '\n== %s\n' "$*"; }

log "init $dir from $subnet"
"$kubit" init "$dir" --name e2e --nodes "$subnet"

log "plan"
"$kubit" plan "$dir" || true

log "apply"
"$kubit" apply "$dir" --yes

log "a second plan has nothing to do"
"$kubit" plan "$dir" --detailed-exitcode

log "credentials derive from the repo"
"$kubit" kubeconfig "$dir" -o "$dir/kubeconfig"
KUBECONFIG="$dir/kubeconfig" kubectl get nodes -o wide

log "etcd snapshot"
"$kubit" etcd snapshot e2e

log "done: $dir"
