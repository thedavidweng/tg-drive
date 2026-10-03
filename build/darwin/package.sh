#!/usr/bin/env bash
# Package td-gui for macOS: a universal (arm64 + amd64) .app bundle wrapped
# in a DMG. Runs only on macOS (lipo, codesign and hdiutil — the latter via
# `wails3 tool package -format dmg`). The bundle is ad-hoc signed
# (`codesign --sign -`): arm64 binaries need a signature to run at all, and
# ad-hoc is what an unsigned release can do. Users still get Gatekeeper on
# first launch; the README documents how to open the app anyway.
#
# Usage: VERSION=1.2.3 build/darwin/package.sh   (version without leading v)
set -euo pipefail

cd "$(dirname "$0")/../.."

VERSION="${VERSION:?set VERSION to the release version without a leading v}"
WAILS3="${WAILS3:-dist/bin/wails3}"
OUT=dist/gui
WORK=dist/gui-darwin-work
. build/version.sh

mkdir -p "$OUT" "$WORK"

# Universal binary. Minimum macOS 12 matches the Wails v3 Taskfiles.
for arch in arm64 amd64; do
	CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" \
		MACOSX_DEPLOYMENT_TARGET=12.0 \
		CGO_CFLAGS="-mmacosx-version-min=12.0" \
		CGO_LDFLAGS="-mmacosx-version-min=12.0" \
		go build -tags gui -trimpath -ldflags "-s -w $VERSION_LDFLAGS" \
		-o "$WORK/td-gui-$arch" ./cmd/td-gui
done
lipo -create -output "$WORK/td-gui" "$WORK/td-gui-arm64" "$WORK/td-gui-amd64"
verify_version "$WORK/td-gui"

# .app bundle.
APP="$WORK/td-gui.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$WORK/td-gui" "$APP/Contents/MacOS/td-gui"
sed "s/@VERSION@/$VERSION/g" build/darwin/Info.plist >"$APP/Contents/Info.plist"
"$WAILS3" generate icons -input assets/icon.png \
	-macfilename "$WORK/icon.icns" -windowsfilename "$WORK/icon.ico"
cp "$WORK/icon.icns" "$APP/Contents/Resources/icon.icns"
codesign --force --deep --sign - "$APP"

# DMG (hdiutil under the hood; expects <out>/<name>.app).
"$WAILS3" tool package -nocolour -format dmg -name td-gui \
	-volume-icon "$WORK/icon.icns" -out "$WORK"
mv "$WORK/td-gui.dmg" "$OUT/td-gui_${VERSION}_darwin_universal.dmg"

cd "$OUT"
# macOS has no sha256sum; shasum ships with the OS.
shasum -a 256 "td-gui_${VERSION}_darwin_universal.dmg" \
	>"td-gui_${VERSION}_darwin_universal.dmg.sha256"
ls -l
