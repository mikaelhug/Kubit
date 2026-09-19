#!/usr/bin/env bash
# Linux lab: Talos amd64 VMs on QEMU/KVM behind one bridge, for CI and any Linux box.
# The bridge carries DHCP (dnsmasq) and NAT to the outside so Talos can pull images;
# VMs see each other and the host, which is what etcd, the VIP and PXE need.
#   lab.sh net up|down             bridge $BRIDGE $SUBNET.1/24, dnsmasq, masquerade
#   lab.sh iso                     download the Talos ISO for $SCHEMATIC/$TALOS_VERSION
#   lab.sh create <n> [cpus] [mem_mib] [disk_gb]
#   lab.sh start <n> [--no-iso]    --no-iso boots the disk (after Talos is installed)
#   lab.sh stop <n>|all
#   lab.sh destroy <n>|all
#   lab.sh ip <n>                  address dnsmasq leased to VM n
#   lab.sh wait <n> [seconds]      until the Talos API answers on VM n
#   lab.sh list
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
STATE=${STATE:-$HERE/state}
TALOS_VERSION=${TALOS_VERSION:-v1.14.0}
SCHEMATIC=${SCHEMATIC:-d9ff89777e246792e7642abd3220a616afb4e49822382e4213a2e528ab826fe5}
ARCH=amd64
ISO=$STATE/talos-$TALOS_VERSION-$ARCH.iso
BRIDGE=${BRIDGE:-kubit0}
SUBNET=${SUBNET:-192.168.105}
MAC_PREFIX=52:54:00:4b:49
QEMU=${QEMU:-qemu-system-x86_64}
OVMF=${OVMF:-$(ls /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/x64/OVMF_CODE.4m.fd 2>/dev/null | head -1)}

mkdir -p "$STATE"
mac_of() { printf '%s:%02x' "$MAC_PREFIX" "$1"; }
dir_of() { echo "$STATE/vm$1"; }
tap_of() { echo "kubit-tap$1"; }
sudo_() { if [ "$(id -u)" = 0 ]; then "$@"; else sudo "$@"; fi; }

cmd_net() {
  case ${1:-} in
    up)
      ip link show "$BRIDGE" >/dev/null 2>&1 || sudo_ ip link add "$BRIDGE" type bridge
      sudo_ ip addr replace "$SUBNET.1/24" dev "$BRIDGE"
      sudo_ ip link set "$BRIDGE" up
      sudo_ sysctl -qw net.ipv4.ip_forward=1
      sudo_ iptables -t nat -C POSTROUTING -s "$SUBNET.0/24" ! -o "$BRIDGE" -j MASQUERADE 2>/dev/null \
        || sudo_ iptables -t nat -A POSTROUTING -s "$SUBNET.0/24" ! -o "$BRIDGE" -j MASQUERADE
      sudo_ iptables -C FORWARD -i "$BRIDGE" -j ACCEPT 2>/dev/null || sudo_ iptables -I FORWARD -i "$BRIDGE" -j ACCEPT
      sudo_ iptables -C FORWARD -o "$BRIDGE" -j ACCEPT 2>/dev/null || sudo_ iptables -I FORWARD -o "$BRIDGE" -j ACCEPT
      if [ ! -f "$STATE/dnsmasq.pid" ] || ! kill -0 "$(cat "$STATE/dnsmasq.pid")" 2>/dev/null; then
        sudo_ dnsmasq --interface="$BRIDGE" --bind-interfaces --except-interface=lo \
          --dhcp-range="$SUBNET.10,$SUBNET.100,12h" --dhcp-option=option:router,"$SUBNET.1" \
          --dhcp-option=option:dns-server,"$SUBNET.1" --dhcp-leasefile="$STATE/leases" \
          --pid-file="$STATE/dnsmasq.pid" --log-facility="$STATE/dnsmasq.log"
        sudo_ chmod 644 "$STATE/leases" "$STATE/dnsmasq.pid" 2>/dev/null || true
      fi
      echo "$BRIDGE up at $SUBNET.1/24";;
    down)
      [ -f "$STATE/dnsmasq.pid" ] && sudo_ kill "$(cat "$STATE/dnsmasq.pid")" 2>/dev/null || true
      rm -f "$STATE/dnsmasq.pid"
      sudo_ iptables -t nat -D POSTROUTING -s "$SUBNET.0/24" ! -o "$BRIDGE" -j MASQUERADE 2>/dev/null || true
      sudo_ ip link del "$BRIDGE" 2>/dev/null || true;;
    *) echo "net up|down" >&2; exit 2;;
  esac
}

cmd_iso() {
  [ -f "$ISO" ] && { echo "$ISO"; return; }
  curl -fsSL -o "$ISO.part" "https://factory.talos.dev/image/$SCHEMATIC/$TALOS_VERSION/metal-$ARCH.iso"
  mv "$ISO.part" "$ISO"
  echo "$ISO"
}

cmd_create() {
  local n=$1 cpus=${2:-2} mem=${3:-2560} disk=${4:-20} d
  d=$(dir_of "$n")
  [ -d "$d" ] && { echo "vm$n already exists" >&2; exit 1; }
  cmd_iso >/dev/null
  mkdir -p "$d"
  echo "$cpus $mem" > "$d/spec"
  qemu-img create -q -f qcow2 "$d/disk.qcow2" "${disk}G"
  cp "$OVMF" "$d/OVMF_CODE.fd" 2>/dev/null || true
  echo "vm$n: $cpus vCPU, $mem MiB, ${disk} GiB, mac $(mac_of "$n")"
}

cmd_start() {
  local n=$1 noiso=${2:-} d cpus mem tap accel
  d=$(dir_of "$n")
  [ -d "$d" ] || { echo "vm$n does not exist" >&2; exit 1; }
  read -r cpus mem < "$d/spec"
  tap=$(tap_of "$n")
  ip link show "$tap" >/dev/null 2>&1 || { sudo_ ip tuntap add "$tap" mode tap user "$(id -un)"; sudo_ ip link set "$tap" master "$BRIDGE"; sudo_ ip link set "$tap" up; }
  accel="-accel kvm -cpu host"; [ -w /dev/kvm ] || accel="-accel tcg -cpu max"
  local drives=(-drive "file=$d/disk.qcow2,if=virtio,format=qcow2")
  [ "$noiso" = --no-iso ] || drives+=(-drive "file=$ISO,media=cdrom,readonly=on")
  nohup "$QEMU" $accel -machine q35 -smp "$cpus" -m "$mem" \
    -drive "if=pflash,format=raw,readonly=on,file=${OVMF:-$d/OVMF_CODE.fd}" \
    "${drives[@]}" \
    -netdev "tap,id=n0,ifname=$tap,script=no,downscript=no" -device "virtio-net-pci,netdev=n0,mac=$(mac_of "$n")" \
    -device virtio-rng-pci -display none -serial "file:$d/console.log" -pidfile "$d/qemu.pid" \
    > "$d/qemu.log" 2>&1 &
  echo "vm$n starting ($(mac_of "$n"))"
}

cmd_stop() {
  local n=$1 d
  if [ "$n" = all ]; then for d in "$STATE"/vm*; do [ -d "$d" ] && cmd_stop "${d##*/vm}"; done; return; fi
  d=$(dir_of "$n")
  [ -f "$d/qemu.pid" ] && kill "$(cat "$d/qemu.pid")" 2>/dev/null || true
  rm -f "$d/qemu.pid"
}

cmd_destroy() {
  local n=$1 d
  if [ "$n" = all ]; then for d in "$STATE"/vm*; do [ -d "$d" ] && cmd_destroy "${d##*/vm}"; done; return; fi
  cmd_stop "$n"
  sudo_ ip link del "$(tap_of "$n")" 2>/dev/null || true
  rm -rf "$(dir_of "$n")"
}

cmd_ip() {
  local mac; mac=$(mac_of "$1")
  awk -v m="$mac" '$2==m {ip=$3} END {print ip}' "$STATE/leases" 2>/dev/null
}

cmd_wait() {
  local n=$1 t=${2:-300} ip
  for ((i=0; i<t; i+=5)); do
    ip=$(cmd_ip "$n")
    if [ -n "$ip" ] && (echo > "/dev/tcp/$ip/50000") 2>/dev/null; then echo "$ip"; return 0; fi
    sleep 5
  done
  echo "vm$n: no Talos API within ${t}s" >&2; tail -20 "$(dir_of "$n")/console.log" >&2 || true; return 1
}

cmd_list() {
  local d n st
  for d in "$STATE"/vm*; do
    [ -d "$d" ] || continue
    n=${d##*/vm}; st=stopped
    [ -f "$d/qemu.pid" ] && kill -0 "$(cat "$d/qemu.pid")" 2>/dev/null && st=running
    printf 'vm%-3s %-8s %s %s\n' "$n" "$st" "$(mac_of "$n")" "$(cmd_ip "$n")"
  done
}

cmd=${1:-}; shift || true
case $cmd in
  net) cmd_net "$@";;
  iso) cmd_iso;;
  create) cmd_create "$@";;
  start) cmd_start "$@";;
  stop) cmd_stop "$@";;
  destroy) cmd_destroy "$@";;
  ip) cmd_ip "$@";;
  wait) cmd_wait "$@";;
  list) cmd_list;;
  *) sed -n '2,14p' "$0"; exit 2;;
esac
