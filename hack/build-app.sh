#!/usr/bin/env bash
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

mode=${1:-local}
dist=${DIST:-dist}
app="$dist/Kubit.app"
identity=${KUBIT_SIGN_IDENTITY:--}
repo=${KUBIT_REPO:-mikaelhug/Kubit}
feed=${KUBIT_UPDATE_FEED:-https://api.github.com/repos/$repo/releases/latest}

case "$mode" in
release)
	tag=$(git describe --tags --exact-match --match 'v[0-9]*.[0-9]*.[0-9]*' 2>/dev/null) || {
		echo "error: HEAD carries no vX.Y.Z tag" >&2
		exit 1
	}
	version=${tag#v}
	[ -s app/release.pub ] || { echo "error: app/release.pub is missing" >&2; exit 1; }
	;;
local) version=${KUBIT_VERSION:-0.0.0} ;;
*) echo "usage: hack/build-app.sh [local|release]" >&2; exit 1 ;;
esac
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "error: version $version is not X.Y.Z" >&2; exit 1; }
pubkey=${KUBIT_RELEASE_PUBKEY:-$(cat app/release.pub 2>/dev/null || true)}

echo "==> web console"
make web

echo "==> kubit $version"
mkdir -p "$dist/bin"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath \
	-ldflags "-s -w -X main.version=v$version" -o "$dist/bin/kubit" ./cmd/kubit

echo "==> KubitApp"
swift build -c release --package-path app --product KubitApp --arch arm64
appbin=$(swift build -c release --package-path app --product KubitApp --arch arm64 --show-bin-path)/KubitApp

echo "==> assemble $app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources/bin"
cp "$appbin" "$app/Contents/MacOS/KubitApp"
cp "$dist/bin/kubit" "$app/Contents/Resources/bin/kubit"
chmod +x "$app/Contents/MacOS/KubitApp" "$app/Contents/Resources/bin/kubit"
if [ -d app/Resources ]; then
	cp -R app/Resources/. "$app/Contents/Resources/"
fi

cat >"$app/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>Kubit</string>
	<key>CFBundleDisplayName</key><string>Kubit</string>
	<key>CFBundleIdentifier</key><string>dev.kubit.Kubit</string>
	<key>CFBundleExecutable</key><string>KubitApp</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>$version</string>
	<key>CFBundleVersion</key><string>$version</string>
	<key>LSMinimumSystemVersion</key><string>27.0</string>
	<key>LSUIElement</key><true/>
	<key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
	<key>NSHighResolutionCapable</key><true/>
	<key>NSLocalNetworkUsageDescription</key><string>Kubit finds and manages Talos machines on your local network.</string>
	<key>KubitReleaseKey</key><string>$pubkey</string>
	<key>KubitUpdateFeed</key><string>$feed</string>
</dict>
</plist>
EOF
plutil -lint "$app/Contents/Info.plist" >/dev/null

codesign_once() {
	if [ "$identity" = "-" ]; then
		codesign --force --sign - "$@"
	else
		codesign --force --options runtime --timestamp --sign "$identity" "$@"
	fi
}

sign() {
	local out attempt
	for attempt in 1 2 3 4 5; do
		if out=$(codesign_once "$@" 2>&1); then
			return 0
		fi
		echo "$out"
		case "$out" in
		*"Operation not permitted"* | *"resource temporarily unavailable"* | *"resource busy"*) sleep "$attempt" ;;
		*) return 1 ;;
		esac
	done
	return 1
}

echo "==> codesign ($identity)"
sign --identifier dev.kubit.kubit "$app/Contents/Resources/bin/kubit"
sign "$app"
codesign --verify --deep --strict "$app"

zip="$dist/Kubit-$version-macos-arm64.zip"
rm -f "$zip"
ditto -c -k --keepParent "$app" "$zip"
for b in "$app/Contents/MacOS/KubitApp" "$app/Contents/Resources/bin/kubit"; do echo "$(basename "$b"): $(lipo -archs "$b")"; done
echo "==> $zip"
