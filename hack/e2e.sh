#!/usr/bin/env bash
# End-to-end run against a live daemon and real machines in maintenance mode:
#   discover → design → create (3 CPs) → add worker → rename → snapshot → verify
#   → [restore] → remove worker → [teardown]
# Every step goes through the API so it shows up in Activity like a user's click.
#
#   hack/e2e.sh 192.168.105.0/24 [--name e2e] [--url http://127.0.0.1:8080]
#               [--with-restore] [--teardown] [--vm-ids "5 6 7 8"]
#
# --teardown removes the worker and forgets the cluster; with --vm-ids it also destroys
# and recreates those hack/vm VMs so the run is repeatable. Needs curl, jq, kubectl.
set -euo pipefail

subnet=${1:?subnet to scan}; shift
name=e2e; url=${KUBIT_URL:-http://127.0.0.1:8080}; with_restore=; teardown=; vm_ids=
while [ $# -gt 0 ]; do
  case $1 in
    --name) name=$2; shift 2;;
    --url) url=$2; shift 2;;
    --with-restore) with_restore=1; shift;;
    --teardown) teardown=1; shift;;
    --vm-ids) vm_ids=$2; shift 2;;
    *) echo "unknown flag $1" >&2; exit 2;;
  esac
done
auth=(); [ -n "${KUBIT_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $KUBIT_TOKEN")
here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

log() { printf '\033[1m[%s] %s\033[0m\n' "$(date +%H:%M:%S)" "$*"; }
api() { # method path [json]
  local m=$1 p=$2 body=${3:-}
  if [ -n "$body" ]; then curl -sS ${auth[@]+"${auth[@]}"} -X "$m" -H 'Content-Type: application/json' -d "$body" "$url/api/v1$p"
  else curl -sS ${auth[@]+"${auth[@]}"} -X "$m" "$url/api/v1$p"; fi
}
wait_op() { # id [timeout-seconds]
  local id=$1 t=${2:-1800} st
  for ((i=0; i<t; i+=5)); do
    st=$(api GET "/operations/$id" | jq -r .status)
    case $st in running) sleep 5;; done) return 0;; *) echo "operation $id $st:" >&2; api GET "/operations/$id" | jq -r '.log' | tail -20 >&2; return 1;; esac
  done
  echo "operation $id timed out" >&2; return 1
}
op() { jq -r .operationId; }

log "daemon $(api GET /version | jq -r .kubit) at $url"
log "discover $subnet"
wait_op "$(api POST /discover "{\"targets\":[\"$subnet\"]}" | op)" 120
macs=(); while IFS= read -r m; do [ -n "$m" ] && macs+=("$m"); done < <(api GET /machines | jq -r '.[] | select(.state=="maintenance" and .cluster=="") | .mac')
[ ${#macs[@]} -ge 4 ] || { echo "need 4 unassigned maintenance machines, found ${#macs[@]}" >&2; exit 1; }
log "using ${macs[*]:0:4}"

log "design + create $name with 3 machines"
design=$(api POST /config/design "$(jq -nc --arg n "$name" --argjson m "$(printf '%s\n' "${macs[@]:0:3}" | jq -R . | jq -sc .)" '{name:$n, macs:$m}')")
echo "$design" | jq -r '.warnings[] | "  lint: \(.level) \(.code) \(.message)"'
yaml=$(echo "$design" | jq -r .yaml)
create=$(api POST /clusters "$(jq -nc --arg y "$yaml" '{yaml:$y, skipPlatform:false}')")
wait_op "$(echo "$create" | op)" 2400
api GET "/clusters/$name/kubeconfig" > "$work/kubeconfig"
export KUBECONFIG=$work/kubeconfig
ready() { kubectl get nodes --no-headers 2>/dev/null | awk '$2=="Ready"' | wc -l | tr -d ' '; }
[ "$(ready)" = 3 ] || { kubectl get nodes; echo "expected 3 Ready nodes" >&2; exit 1; }
log "3 control planes Ready"

log "add ${macs[3]} as worker"
m4=$(api GET "/machines/${macs[3]}")
node=$(echo "$m4" | jq -c --arg n "$name" '{hostname: ($n+"-worker-01"), ip: .ip, mac: .mac, uuid: .uuid, pool: "worker", arch: .arch, kvm: (.inventory.kvm // false), installDisk: {path: ([.inventory.disks[] | select(.readonly|not) | select(.cdrom|not)] | sort_by(-.sizeBytes) | .[0].devPath)}}')
wait_op "$(api POST "/clusters/$name/nodes" "$node" | op)" 1200
[ "$(ready)" = 4 ] || { kubectl get nodes; echo "expected 4 Ready nodes" >&2; exit 1; }
log "worker joined"

log "rename worker → $name-batch-01"
wait_op "$(api POST "/clusters/$name/nodes/$name-worker-01/rename" '{"to":"'"$name"'-batch-01"}' | op)" 600
kubectl get node "$name-batch-01" >/dev/null && ! kubectl get node "$name-worker-01" >/dev/null 2>&1 || { echo "rename left a stale Node" >&2; exit 1; }
log "renamed; no stale Node object"

log "etcd snapshot + verify"
wait_op "$(api POST "/clusters/$name/snapshots" '{"source":"manual"}' | op)" 300
sid=$(api GET "/clusters/$name/snapshots" | jq -r '.[0].id')
api POST "/clusters/$name/snapshots/$sid/verify" | jq -e '.ok' >/dev/null || { echo "snapshot verify failed" >&2; exit 1; }
log "snapshot #$sid verified"

if [ -n "$with_restore" ]; then
  log "restore drill from snapshot #$sid"
  kubectl create ns e2e-marker >/dev/null
  wait_op "$(api POST "/clusters/$name/snapshots/$sid/restore?ignoreWindow=true" '{"confirm":"'"$name"'"}' | op)" 1800
  kubectl get ns e2e-marker >/dev/null 2>&1 && { echo "namespace created after the snapshot survived the restore" >&2; exit 1; }
  [ "$(ready)" = 4 ] || { kubectl get nodes; echo "expected 4 Ready nodes after restore" >&2; exit 1; }
  log "restore drill passed"
fi

log "service health: crashloop must alert (10 min quiet window after the restore, then ≤ 7 min)"
kubectl create deploy e2e-crash --image=busybox -- sh -c 'exit 1' >/dev/null 2>&1
for ((i=0; i<130; i++)); do
  api GET "/clusters/$name/events?unacked=true" | jq -e '[.[] | select(.kind=="pod.crashloop" and (.node|test("e2e-crash")))] | length > 0' >/dev/null && break
  sleep 10
done
api GET "/clusters/$name/events?unacked=true" | jq -e '[.[] | select(.kind=="pod.crashloop" and (.node|test("e2e-crash")))] | length > 0' >/dev/null || { echo "no pod.crashloop alert" >&2; exit 1; }
api GET "/clusters/$name/events?unacked=true" | jq -e '[.[] | select(.severity!="info" and (.node|tostring|test("e2e-crash")|not))] | length == 0' >/dev/null || { echo "unexpected alerts after a clean create/restore:" >&2; api GET "/clusters/$name/events?unacked=true" | jq -r '.[] | select(.severity!="info") | "  \(.kind) \(.node) \(.message)"' >&2; exit 1; }
kubectl delete deploy e2e-crash >/dev/null
log "crashloop alert raised"

log "remove the worker"
wait_op "$(api DELETE "/clusters/$name/nodes/$name-batch-01" | op)" 900
[ "$(ready)" = 3 ] || { kubectl get nodes; echo "expected 3 nodes after remove" >&2; exit 1; }
for ((i=0; i<60; i++)); do
  [ "$(api GET "/machines/${macs[3]}" | jq -r .state)" = maintenance ] && break; sleep 5
done
[ "$(api GET "/machines/${macs[3]}" | jq -r .state)" = maintenance ] || { echo "removed machine did not return to maintenance" >&2; exit 1; }
log "worker back in maintenance mode"

if [ -n "$teardown" ]; then
  log "teardown: forget $name"
  api DELETE "/clusters/$name" >/dev/null
  if [ -n "$vm_ids" ]; then
    for v in $vm_ids; do "$here/vm/vm.sh" destroy "$v" >/dev/null; "$here/vm/vm.sh" create "$v" 2 2304 >/dev/null; "$here/vm/vm.sh" start "$v" >/dev/null; done
    log "VMs $vm_ids recreated"
  fi
fi
log "e2e passed"
