#!/usr/bin/env bash
# Dev harness: Talos arm64 VMs on Apple Virtualization.framework via vfkit.
# Usage:
#   vm.sh iso                      download Talos ISO for $SCHEMATIC/$TALOS_VERSION
#   vm.sh create <n> [cpus] [mem]  create VM number n (1..99), boot from ISO, run in background
#   vm.sh start <n> [--no-iso]     (re)start VM; --no-iso boots from disk after Talos install
#   vm.sh stop <n> | stop all
#   vm.sh ip <n>                   IP leased to VM n (from vmnet's dhcpd leases)
#   vm.sh list
#   vm.sh destroy <n> | destroy all
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
STATE=${STATE:-$HERE/state}
TALOS_VERSION=${TALOS_VERSION:-v1.14.0}
SCHEMATIC=${SCHEMATIC:-d9ff89777e246792e7642abd3220a616afb4e49822382e4213a2e528ab826fe5} # siderolabs/gvisor
ARCH=arm64
ISO=$STATE/talos-$TALOS_VERSION-$ARCH.iso
DISK_GB=${DISK_GB:-20}
MAC_PREFIX=52:54:00:4b:49  # "KI"
# vfkit's built-in NAT isolates VMs from each other (peers get "no route to host"),
# which breaks etcd joins and any L2 VIP. vmnet-helper (brew tap nirs/vmnet-helper;
# no root needed on macOS 26) drives vmnet directly with isolation off, so VMs sharing
# the subnet below reach each other and the host. NET=nat forces plain NAT.
VMNET_RUN=${VMNET_RUN:-/opt/homebrew/opt/vmnet-helper/libexec/vmnet-run}
VMNET_START=${VMNET_START:-192.168.105.1}
VMNET_END=${VMNET_END:-192.168.105.100}
VMNET_MASK=${VMNET_MASK:-255.255.255.0}

mkdir -p "$STATE"

mac_of()   { printf '%s:%02x' "$MAC_PREFIX" "$1"; }
dir_of()   { echo "$STATE/vm$1"; }
port_of()  { echo $((8100 + $1)); }

use_vmnet() { [ "${NET:-}" != nat ] && [ -x "$VMNET_RUN" ]; }

net_device() {
  if use_vmnet; then
    echo "virtio-net,fd=4,mac=$(mac_of "$1")"
  else
    echo "virtio-net,nat,mac=$(mac_of "$1")"
  fi
}

cmd_iso() {
  [ -f "$ISO" ] && { echo "$ISO"; return; }
  curl -fsSL -o "$ISO.part" \
    "https://factory.talos.dev/image/$SCHEMATIC/$TALOS_VERSION/metal-$ARCH.iso"
  mv "$ISO.part" "$ISO"
  echo "$ISO"
}

cmd_create() {
  local n=$1 cpus=${2:-2} mem=${3:-4096} d
  d=$(dir_of "$n")
  [ -d "$d" ] && { echo "vm$n already exists" >&2; exit 1; }
  cmd_iso >/dev/null
  mkdir -p "$d"
  echo "$cpus $mem" > "$d/spec"
  truncate -s "${DISK_GB}G" "$d/disk.raw"
  cmd_start "$n"
}

cmd_start() {
  local n=$1 noiso=${2:-} d cpus mem
  d=$(dir_of "$n")
  [ -d "$d" ] || { echo "vm$n does not exist" >&2; exit 1; }
  if [ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null; then
    echo "vm$n already running (pid $(cat "$d/pid"))"; return
  fi
  read -r cpus mem < "$d/spec"
  local args=(
    --cpus "$cpus" --memory "$mem"
    --bootloader "efi,variable-store=$d/efi-vars,create"
    --device "virtio-blk,path=$d/disk.raw"
    --device "$(net_device "$n")"
    --device "virtio-serial,logFilePath=$d/console.log"
    --device virtio-rng
    --restful-uri "tcp://127.0.0.1:$(port_of "$n")"
  )
  [ "$noiso" = "--no-iso" ] || args+=(--device "usb-mass-storage,path=$ISO,readonly")
  if use_vmnet; then
    nohup "$VMNET_RUN" --operation-mode shared --start-address "$VMNET_START" \
      --end-address "$VMNET_END" --subnet-mask "$VMNET_MASK" -- vfkit "${args[@]}" > "$d/vfkit.log" 2>&1 &
  else
    nohup vfkit "${args[@]}" > "$d/vfkit.log" 2>&1 &
  fi
  echo $! > "$d/pid"
  echo "vm$n started (pid $!, mac $(mac_of "$n"), rest :$(port_of "$n"))"
}

cmd_stop() {
  local n=$1 d
  if [ "$n" = all ]; then for d in "$STATE"/vm*; do [ -d "$d" ] && cmd_stop "${d##*/vm}"; done; return; fi
  d=$(dir_of "$n")
  [ -f "$d/pid" ] || return 0
  local pid; pid=$(cat "$d/pid")
  kill "$pid" 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done
  rm -f "$d/pid"
  echo "vm$n stopped"
}

cmd_ip() {
  local mac
  mac=$(mac_of "$1" | sed 's/:0\([0-9a-f]\)/:\1/g')  # dhcpd_leases strips leading zeros
  awk -v mac="$mac" '
    /^\{/ {ip=""; hw=""}
    /ip_address=/ {sub(/.*ip_address=/, ""); ip=$0}
    /hw_address=/ {sub(/.*hw_address=1,/, ""); hw=$0}
    /^\}/ { if (hw == mac) print ip }
  ' /var/db/dhcpd_leases 2>/dev/null | head -1
}

cmd_list() {
  local d n st
  for d in "$STATE"/vm*; do
    [ -d "$d" ] || continue
    n=${d##*/vm}
    if [ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null; then st=running; else st=stopped; fi
    printf 'vm%-3s %-8s mac=%s ip=%s\n' "$n" "$st" "$(mac_of "$n")" "$(cmd_ip "$n")"
  done
}

cmd_destroy() {
  local n=$1
  if [ "$n" = all ]; then for d in "$STATE"/vm*; do [ -d "$d" ] && cmd_destroy "${d##*/vm}"; done; return; fi
  cmd_stop "$n"
  rm -rf "$(dir_of "$n")"
  echo "vm$n destroyed"
}

case ${1:-} in
  iso)     cmd_iso ;;
  create)  cmd_create "${@:2}" ;;
  start)   cmd_start "${@:2}" ;;
  stop)    cmd_stop "${2:?n|all}" ;;
  ip)      cmd_ip "${2:?n}" ;;
  list)    cmd_list ;;
  destroy) cmd_destroy "${2:?n|all}" ;;
  *)       sed -n '2,12p' "$0"; exit 1 ;;
esac
