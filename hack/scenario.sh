#!/usr/bin/env bash
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
kubit=${KUBIT:-$here/../bin/kubit}
work=${WORK:-$(mktemp -d)}
port=${PORT:-8099}
subnet=${SUBNET:-192.168.105.0/24}
gateway=${GATEWAY:-192.168.105.1}
prefix=${subnet%.*}
api=http://127.0.0.1:$port/api/v1
name=e2e
macs=(52:54:00:4b:49:01 52:54:00:4b:49:02 52:54:00:4b:49:03)

mkdir -p "$work/home"
export HOME=$work/home
unset SOPS_AGE_KEY SOPS_AGE_KEY_FILE XDG_CONFIG_HOME KUBECONFIG
kc=$work/kubeconfig
step=0

log() { step=$((step + 1)); printf '\n== %d. %s (%s)\n' "$step" "$*" "$(date +%T)"; }
fail() {
  printf '\nFAIL: %s\n' "$*" >&2
  if [[ $* == "timed out"* ]]; then stacks; fi
  curl -fsS "$api/clusters/$name/apply" 2>/dev/null | jq -r '.lines[-25:][]? | "\(.time // "") \(.step // "") \(.message // .)"' >&2 || true
  echo "work dir: $work; daemon log: $work/serve.log" >&2
  exit 1
}
get() { curl -fsS "$api$1"; }
send() {
  local out code
  out=$(curl -sS -X "$1" -H 'Content-Type: application/json' ${3:+-d "$3"} -w $'\n%{http_code}' "$api$2")
  code=${out##*$'\n'}
  out=${out%$'\n'*}
  [[ $code == 2* ]] || fail "$1 $2: $code $out"
  printf '%s' "$out"
}
until_ok() {
  local what=$1 limit=$2 t=0
  shift 2
  until "$@"; do
    t=$((t + 5))
    ((t < limit)) || fail "timed out after ${limit}s: $what"
    sleep 5
  done
}

row_hash() { get /clusters | jq -r --arg n "$name" '.[] | select(.name == $n) | .hash'; }
plan_summary() { get "/clusters/$name/plan" | jq -c .summary; }

planned_after() {
  local s
  s=$(plan_summary)
  case $(jq -r .state <<<"$s") in
    ready | blocked | failed) [[ $(jq -r .plannedAt <<<"$s") > $1 ]] ;;
    *) return 1 ;;
  esac
}

stacks() { if [[ -n ${daemon:-} ]] && kill -QUIT "$daemon" 2>/dev/null; then sleep 1; echo "goroutine dump in $work/serve.log" >&2; fi; }

fresh_plan() {
  local since s
  since=$(date -u +%FT%TZ)
  sleep 1
  send POST "/clusters/$name/plan" >/dev/null
  until_ok "a plan after $since" 900 planned_after "$since"
  s=$(plan_summary)
  case $(jq -r .state <<<"$s") in
    blocked) fail "plan blocked: $(get "/clusters/$name/plan" | jq -c '.plan.problems')" ;;
    failed) fail "plan failed: $(jq -r .error <<<"$s")" ;;
  esac
  printf '%s' "$s"
}

apply_done() { [[ $(get "/clusters/$name/apply" | jq -r '.running') == false ]]; }

apply() {
  local removal=${1:-false} s run
  s=$(fresh_plan)
  jq -r '"   plan: \(.changes) change(s), \(.oneTime) one-time"' <<<"$s"
  get "/clusters/$name/plan" | jq -r '.plan.changes[]? | "     \(.action) \(.node // "") \((.detail // "") | split("\n")[0])"'
  send POST "/clusters/$name/apply" "$(jq -nc --arg h "$(jq -r .hash <<<"$s")" --argjson r "$removal" '{planHash: $h, allowRemoval: $r}')" >/dev/null
  until_ok "the apply to finish" 3600 apply_done
  run=$(get "/clusters/$name/apply")
  [[ -z $(jq -r '.error // empty' <<<"$run") ]] || fail "apply: $(jq -r .error <<<"$run")"
  s=$(fresh_plan)
  [[ $(jq -r '.changes + .oneTime + .problems' <<<"$s") == 0 ]] || fail "the plan after apply is not empty: $s $(get "/clusters/$name/plan" | jq -c '[.plan.changes[]? | {action, node, detail: ((.detail // "") | split("\n")[0])}]')"
  [[ -z $(jq -r '.holder // empty' <<<"$s") ]] || fail "the apply lock is still held: $s"
  get "/clusters/$name/kubeconfig" >"$kc"
  kubectl --kubeconfig "$kc" wait --for=condition=Ready nodes --all --timeout=600s >/dev/null || fail "nodes not Ready"
  [[ -z $(kubectl --kubeconfig "$kc" -n kube-system get lease kubit-apply -o jsonpath='{.spec.holderIdentity}' 2>/dev/null) ]] || fail "kubit-apply Lease still held"
  echo "   applied; plan empty; lock free; nodes Ready"
}

ip_of_mac() { get /nodes | jq -r --arg m "$1" '.[] | select(.mac == $m) | .ip'; }
host_of_mac() { yq -r ".spec.nodes[] | select(.mac == \"$1\") | .hostname" "$work/$name/cluster.yaml" 2>/dev/null || grep -B2 "$1" "$work/$name/cluster.yaml" | awk '/hostname:/ {print $3}'; }
internal_ip() { kubectl --kubeconfig "$kc" get node "$1" -o jsonpath='{.status.addresses[?(@.type=="InternalIP")].address}'; }
expect_ip() {
  local got
  got=$(internal_ip "$1")
  [[ $got == "$2" ]] || fail "$1 has InternalIP $got, want $2"
  grep -A1 "hostname: $1" "$work/$name/cluster.yaml" | grep -q "ip: $2" || fail "cluster.yaml does not record $1 at $2"
  echo "   $1 is at $2"
}

set_network() {
  local host=$1 body=$2 hash
  hash=$(get "/clusters/$name/nodes/$host/network" | jq -r .hash)
  send PUT "/clusters/$name/nodes/$host/network" "$(jq -c --arg h "$hash" '. + {hash: $h}' <<<"$body")" >/dev/null
}

static_at() { jq -nc --arg a "$1/24" --arg g "$gateway" '{static: true, address: $a, gateway: $g, nameservers: [$g]}'; }

discovered() { (($(get /nodes | jq --argjson m "$(printf '%s\n' "${macs[@]}" | jq -R . | jq -s .)" '[.[] | select(.kind == "maintenance" and (.mac as $x | $m | index($x)))] | length') == ${#macs[@]})); }

cleanup() { if [[ -n ${daemon:-} ]]; then kill "$daemon" 2>/dev/null || true; fi; }
trap cleanup EXIT

printf 'discoverySubnets: [%s]\n' "$subnet" >"$work/kubit.yaml"
repos=()
[[ -d $work/$name ]] && repos=("$work/$name")
"$kubit" serve --addr "127.0.0.1:$port" --config "$work/kubit.yaml" ${repos[@]+"${repos[@]}"} >>"$work/serve.log" 2>&1 &
daemon=$!
until_ok "the daemon" 60 curl -fsS -o /dev/null "$api/clusters"

hosts() {
  cp=$(host_of_mac "${macs[0]}")
  w1=$(host_of_mac "${macs[1]}")
  w2=$(host_of_mac "${macs[2]}")
  get "/clusters/$name/kubeconfig" >"$kc"
}

step_1() {
  log "discover ${#macs[@]} machines in maintenance mode"
  send POST /discover "$(jq -nc --arg s "$subnet" '{targets: [$s]}')" >/dev/null
  until_ok "discovery" 600 discovered
  for m in "${macs[@]}"; do echo "   $m at $(ip_of_mac "$m")"; done
}

step_2() {
  log "create a repo for one control plane and one worker"
  local view
  view=$(send POST /repos "$(jq -nc --arg d "$work/$name" --arg n "$name" --arg a "${macs[0]}" --arg b "${macs[1]}" '{dir: $d, name: $n, machines: [{mac: $a, role: "controlplane"}, {mac: $b, role: "worker"}]}')")
  jq -r '"   endpoint \(.endpoint), vip \(.vip // "none"), Talos \(.talosVersion), Kubernetes \(.kubernetesVersion)"' <<<"$view"
  until_ok "the repo to be served" 60 bash -c "curl -fsS '$api/clusters' | jq -e '.[] | select(.name == \"$name\")' >/dev/null"
  jq -r '"\(.talosVersion) \(.kubernetesVersion)"' <<<"$view" >"$work/targets"
}

step_3() {
  log "edit cluster.yaml in the editor: no VIP, older Talos and Kubernetes"
  local file yaml stale old_k8s
  read -r _ target_k8s <"$work/targets"
  file=$(get "/clusters/$name/yaml")
  old_k8s=$(sed -E 's/^(v[0-9]+\.[0-9]+)\.([0-9]+)$/\1.0/' <<<"$target_k8s")
  yaml=$(jq -r .yaml <<<"$file" | sed -E "/^ *vip:/d; /^ *endpoint:/d; s/^( *talosVersion:).*/\1 v1.14.0/; s/^( *kubernetesVersion:).*/\1 $old_k8s/")
  send PUT "/clusters/$name/yaml" "$(jq -nc --arg y "$yaml" --arg h "$(jq -r .hash <<<"$file")" '{yaml: $y, hash: $h}')" >/dev/null
  stale=$(curl -s -o /dev/null -w '%{http_code}' -X PUT -d "$(jq -nc --arg y "$yaml" '{yaml: $y, hash: "stale"}')" "$api/clusters/$name/yaml")
  [[ $stale == 409 ]] || fail "a stale cluster.yaml save answered $stale"
  echo "   Talos v1.14.0, Kubernetes $old_k8s; a stale save is refused"
}

step_4() {
  log "apply: create the cluster"
  apply
}

step_5() {
  log "add the third machine as a worker"
  local design
  design=$(send POST /design "$(jq -nc --arg c "$name" --arg m "${macs[2]}" '{cluster: $c, machines: [{mac: $m, role: "worker"}]}')")
  send POST "/clusters/$name/nodes" "$(jq -nc --arg m "${macs[2]}" --arg h "$(jq -r .hash <<<"$design")" '{machines: [{mac: $m, role: "worker"}], hash: $h}')" >/dev/null
  apply
}

step_6() {
  hosts
  log "pin $w1 static at its current address"
  local ip
  ip=$(internal_ip "$w1")
  set_network "$w1" "$(static_at "$ip")"
  apply
  expect_ip "$w1" "$ip"
}

step_7() {
  hosts
  log "move $w1 to $prefix.150"
  set_network "$w1" "$(static_at "$prefix.150")"
  apply
  expect_ip "$w1" "$prefix.150"
}

step_8() {
  hosts
  log "move the control plane $cp to $prefix.151 with the endpoint"
  set_network "$cp" "$(static_at "$prefix.151")"
  grep -q "endpoint: https://$prefix.151:6443" "$work/$name/cluster.yaml" || fail "the endpoint did not follow the control plane"
  apply
  expect_ip "$cp" "$prefix.151"
  grep -q "$prefix.151:6443" "$kc" || fail "the console kubeconfig still points at the old endpoint"
}

step_9() {
  hosts
  log "release $w1 back to DHCP"
  set_network "$w1" '{"static": false}'
  apply
}

step_10() {
  local target_talos target_k8s
  read -r target_talos target_k8s <"$work/targets"
  log "upgrade to Talos $target_talos and Kubernetes $target_k8s"
  send PUT "/clusters/$name/versions" "$(jq -nc --arg t "$target_talos" --arg k "$target_k8s" --arg h "$(row_hash)" '{talosVersion: $t, kubernetesVersion: $k, hash: $h}')" >/dev/null
  apply
}

step_11() {
  log "toggle metrics-server off and on"
  local on want
  on=$(get /clusters | jq -r --arg n "$name" '.[] | select(.name == $n) | .spec.spec.platform.metricsServer.enabled')
  for want in $([[ $on == true ]] && echo "false true" || echo "true false"); do
    send PUT "/clusters/$name/platform/metricsServer" "$(jq -nc --argjson e "$want" --arg h "$(row_hash)" '{enabled: $e, hash: $h}')" >/dev/null
    apply
  done
}

step_12() {
  log "take an etcd snapshot"
  send POST "/clusters/$name/snapshots" >/dev/null
  get "/clusters/$name/snapshots" | jq -r '.[0] | "   \(.id) \(.sizeBytes) bytes"'
}

step_13() {
  hosts
  log "remove $w2"
  send DELETE "/clusters/$name/nodes/$w2?hash=$(row_hash)" >/dev/null
  apply true
  if kubectl --kubeconfig "$kc" get node "$w2" >/dev/null 2>&1; then fail "$w2 is still a Kubernetes node"; fi
}

start=${START:-1}
for n in $(seq "$start" 13); do
  step=$((n - 1))
  "step_$n"
done

log "done"
echo "work dir: $work"
