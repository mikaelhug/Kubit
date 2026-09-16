#!/usr/bin/env bash
# Dev harness: a Debian lab host as an arm64 VM on Apple Virtualization.framework
# (vfkit, nested virtualisation), installed exactly the way Kubit installs a real
# one — same preseed, same post-install — minus PXE. The installer must run in UEFI
# mode (partman-efi and grub-efi need it, and the disk has to boot under EFI later),
# and vfkit's direct kernel loader is not EFI, so a small FAT boot volume carries
# systemd-boot + the netboot kernel/initrd + one entry with the preseed URL.
#
# Prerequisites: `kubit serve` on :8090 and, in another terminal,
#   kubit pxe --http-only --ip 192.168.105.1 --kubit-url http://127.0.0.1:8090
# (192.168.105.1 is vmnet's host side; the interface only exists while a VM runs,
# hence --ip instead of --iface).
#
# Usage:
#   lab.sh create <n> [cpus] [mem_mib] [disk_gib]   register the MAC in Kubit, run Make lab host (manual), boot the installer (4 vCPU / 12 GiB / 40 GiB; 4 Talos VMs × 2 GiB)
#   lab.sh start <n>            boot from disk (after the install)
#   lab.sh stop <n> | stop all
#   lab.sh console <n>          follow the serial console (installer output)
#   lab.sh route <n>            route 192.168.123.0/24 (the VMs) via the lab host (sudo)
#   lab.sh ip <n> | list | destroy <n> | destroy all
set -euo pipefail

HERE=$(cd "$(dirname "$0")" && pwd)
STATE=${STATE:-$HERE/state}
KUBIT=${KUBIT:-http://127.0.0.1:8090}
PXE=${PXE:-http://192.168.105.1:8069}
SUITE=${SUITE:-trixie}
MIRROR=${MIRROR:-http://deb.debian.org/debian}
ARCH=arm64
MAC_PREFIX=52:54:00:4c:41  # "LA"
VMNET_RUN=${VMNET_RUN:-/opt/homebrew/opt/vmnet-helper/libexec/vmnet-run}
VMNET_START=${VMNET_START:-192.168.105.1}
VMNET_END=${VMNET_END:-192.168.105.100}
VMNET_MASK=${VMNET_MASK:-255.255.255.0}

mkdir -p "$STATE"
mac_of()  { printf '%s:%02x' "$MAC_PREFIX" "$1"; }
dir_of()  { echo "$STATE/lab$1"; }
port_of() { echo $((8200 + $1)); }
use_vmnet() { [ "${NET:-}" != nat ] && [ -x "$VMNET_RUN" ]; }
net_device() { if use_vmnet; then echo "virtio-net,fd=4,mac=$(mac_of "$1")"; else echo "virtio-net,nat,mac=$(mac_of "$1")"; fi; }

cmd_netboot() {
  local base="$MIRROR/dists/$SUITE/main/installer-$ARCH/current/images/netboot/debian-installer/$ARCH"
  for f in linux initrd.gz; do
    if [ ! -s "$STATE/$f" ]; then
      curl -fsSL -o "$STATE/$f.part" "$base/$f" && mv -f "$STATE/$f.part" "$STATE/$f"
    fi
  done
  if [ ! -s "$STATE/systemd-bootaa64.efi" ]; then
    local deb
    deb=$(curl -fsSL "$MIRROR/pool/main/s/systemd/" | grep -o 'systemd-boot-efi_[^"]*_arm64\.deb' | sort -V | tail -1)
    [ -n "$deb" ] || { echo "systemd-boot-efi not found in the pool" >&2; return 1; }
    local tmp; tmp=$(mktemp -d)
    curl -fsSL -o "$tmp/sd.deb" "$MIRROR/pool/main/s/systemd/$deb"
    (cd "$tmp" && ar x sd.deb && tar -xf data.tar.* ./usr/lib/systemd/boot/efi/systemd-bootaa64.efi)
    cp "$tmp/usr/lib/systemd/boot/efi/systemd-bootaa64.efi" "$STATE/systemd-bootaa64.efi"
    rm -rf "$tmp"
  fi
  [ -s "$STATE/linux" ] && [ -s "$STATE/initrd.gz" ] && [ -s "$STATE/systemd-bootaa64.efi" ]
}

# boot_volume builds the FAT image the EFI firmware boots the installer from.
boot_volume() {
  local d=$1 cmdline=$2 img="$1/boot.img" mnt
  rm -f "$img"
  hdiutil create -quiet -size 256m -fs "MS-DOS FAT32" -volname KUBITBOOT -layout NONE "$img"
  mv "$img.dmg" "$img"
  mnt=$(hdiutil attach -nobrowse -readwrite "$img" | awk '/\/Volumes\//{print $NF; exit}')
  mkdir -p "$mnt/EFI/BOOT" "$mnt/loader/entries"
  cp "$STATE/systemd-bootaa64.efi" "$mnt/EFI/BOOT/BOOTAA64.EFI"
  cp "$STATE/linux" "$mnt/linux"
  cp "$STATE/initrd.gz" "$mnt/initrd.gz"
  printf 'default debian\ntimeout 1\neditor no\n' > "$mnt/loader/loader.conf"
  printf 'title Debian installer (Kubit lab host)\nlinux /linux\ninitrd /initrd.gz\noptions %s\n' "$cmdline" > "$mnt/loader/entries/debian.conf"
  hdiutil detach -quiet "$mnt"
}

run_vfkit() {
  local n=$1 d; shift
  d=$(dir_of "$n")
  if use_vmnet; then
    nohup "$VMNET_RUN" --operation-mode shared --start-address "$VMNET_START" \
      --end-address "$VMNET_END" --subnet-mask "$VMNET_MASK" -- vfkit "$@" > "$d/vfkit.log" 2>&1 &
  else
    nohup vfkit "$@" > "$d/vfkit.log" 2>&1 &
  fi
  echo $! > "$d/pid"
}

common_args() {
  local n=$1 d cpus mem
  d=$(dir_of "$n"); read -r cpus mem < "$d/spec"
  printf '%s\n' --cpus "$cpus" --memory "$mem" --nested \
    --device "virtio-blk,path=$d/disk.raw" \
    --device "$(net_device "$n")" \
    --device "virtio-serial,logFilePath=$d/console.log" \
    --device virtio-rng \
    --restful-uri "tcp://127.0.0.1:$(port_of "$n")"
}

cmd_create() {
  local n=$1 cpus=${2:-4} mem=${3:-12288} disk=${4:-40} d mac
  d=$(dir_of "$n"); mac=$(mac_of "$n")
  [ -d "$d" ] && { echo "lab$n already exists" >&2; exit 1; }
  cmd_netboot || { echo "could not fetch the Debian $SUITE $ARCH netboot installer" >&2; exit 1; }
  mkdir -p "$d"
  echo "$cpus $mem" > "$d/spec"
  truncate -s "${disk}G" "$d/disk.raw"
  # Register the machine and let Kubit arm the install; Kubit prints the boot line,
  # which is what we boot with (the same cmdline the PXE script would carry).
  curl -fsS -X POST "$KUBIT/api/v1/machines" -H 'Content-Type: application/json' \
    -d "{\"mac\":\"$mac\",\"hostname\":\"lab$n\",\"arch\":\"$ARCH\"}" > /dev/null
  # vmnet drops frames from MACs other than the VM's own, so the nested Talos VMs
  # cannot sit on the bridge; they go on a routed libvirt network and the Mac needs a route.
  local plan=${PLAN:-'{"manual":true,"network":"routed","vms":{"each":[{"role":"controlplane","cpus":2,"memMiB":2048,"diskGiB":20},{"role":"worker","cpus":2,"memMiB":2048,"diskGiB":20},{"role":"worker","cpus":2,"memMiB":2048,"diskGiB":20},{"role":"worker","cpus":2,"memMiB":2048,"diskGiB":20}]},"cluster":{"name":"lab","controlPlanes":1}}'}
  if [ "${NO_PLAN:-}" = 1 ]; then plan='{"manual":true}'; fi
  local op
  op=$(curl -fsS -X POST "$KUBIT/api/v1/machines/$mac/labhost" -H 'Content-Type: application/json' -d "$plan") || {
    echo "Make lab host refused; is 'kubit pxe --http-only' running? ($op)" >&2; exit 1; }
  echo "kubit operation: $op"
  local cmdline="auto=true priority=critical url=$PXE/labhost/$mac/preseed?arch=$ARCH interface=auto netcfg/get_hostname=lab$n netcfg/get_domain=lab DEBIAN_FRONTEND=text console=hvc0 ---"
  boot_volume "$d" "$cmdline"
  local args=()
  while IFS= read -r a; do args+=("$a"); done < <(common_args "$n")
  # The boot volume comes first so the firmware picks it; the empty disk is second.
  run_vfkit "$n" --bootloader "efi,variable-store=$d/efi-vars,create" --device "virtio-blk,path=$d/boot.img" "${args[@]}"
  echo "lab$n installing (pid $(cat "$d/pid"), mac $mac); follow with: $0 console $n"
  echo "when it has an address (lab.sh ip $n), give the Mac a route to its VMs:  $0 route $n"
}

cmd_start() {
  local n=$1 d
  d=$(dir_of "$n")
  [ -d "$d" ] || { echo "lab$n does not exist" >&2; exit 1; }
  if [ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null; then echo "lab$n already running"; return; fi
  local args=()
  while IFS= read -r a; do args+=("$a"); done < <(common_args "$n")
  run_vfkit "$n" --bootloader "efi,variable-store=$d/efi-vars,create" "${args[@]}"
  echo "lab$n started from disk (pid $(cat "$d/pid"))"
}

cmd_stop() {
  local n=$1 d
  if [ "$n" = all ]; then for d in "$STATE"/lab*; do [ -d "$d" ] && cmd_stop "${d##*/lab}"; done; return; fi
  d=$(dir_of "$n")
  [ -f "$d/pid" ] || return 0
  local pid; pid=$(cat "$d/pid")
  kill "$pid" 2>/dev/null || true
  while kill -0 "$pid" 2>/dev/null; do sleep 0.2; done
  rm -f "$d/pid"
  echo "lab$n stopped"
}

# The installer and the installed system take separate leases; Kubit knows the
# current one, the leases file is the fallback (newest last).
cmd_ip() {
  local mac ip
  ip=$(curl -fsS "$KUBIT/api/v1/machines/$(mac_of "$1")" 2>/dev/null | python3 -c 'import json,sys; print(json.load(sys.stdin).get("ip",""))' 2>/dev/null || true)
  [ -n "$ip" ] && { echo "$ip"; return; }
  mac=$(mac_of "$1" | sed 's/:0\([0-9a-f]\)/:\1/g')
  awk -v mac="$mac" '
    /^\{/ {ip=""; hw=""}
    /ip_address=/ {sub(/.*ip_address=/, ""); ip=$0}
    /hw_address=/ {sub(/.*hw_address=1,/, ""); hw=$0}
    /^\}/ { if (hw == mac) print ip }
  ' /var/db/dhcpd_leases 2>/dev/null | tail -1
}

cmd_console() { tail -n 40 -f "$(dir_of "$1")/console.log"; }

# route sends the Mac's traffic for the NAT VM subnet through the lab host.
cmd_route() {
  local ip; ip=$(cmd_ip "$1")
  [ -n "$ip" ] || { echo "lab$1 has no lease yet" >&2; exit 1; }
  sudo route -n delete 192.168.123.0/24 >/dev/null 2>&1 || true
  sudo route -n add 192.168.123.0/24 "$ip"
}

cmd_list() {
  local d n st
  for d in "$STATE"/lab*; do
    [ -d "$d" ] || continue
    n=${d##*/lab}
    if [ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null; then st=running; else st=stopped; fi
    printf 'lab%-3s %-8s mac=%s ip=%s\n' "$n" "$st" "$(mac_of "$n")" "$(cmd_ip "$n")"
  done
}

cmd_destroy() {
  local n=$1
  if [ "$n" = all ]; then for d in "$STATE"/lab*; do [ -d "$d" ] && cmd_destroy "${d##*/lab}"; done; return; fi
  cmd_stop "$n"
  rm -rf "$(dir_of "$n")"
  echo "lab$n destroyed"
}

case ${1:-} in
  create)  cmd_create "${@:2}" ;;
  start)   cmd_start "${2:?n}" ;;
  stop)    cmd_stop "${2:?n|all}" ;;
  console) cmd_console "${2:?n}" ;;
  route)   cmd_route "${2:?n}" ;;
  ip)      cmd_ip "${2:?n}" ;;
  list)    cmd_list ;;
  destroy) cmd_destroy "${2:?n|all}" ;;
  *)       sed -n '2,17p' "$0"; exit 1 ;;
esac
