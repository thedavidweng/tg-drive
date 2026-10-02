#!/usr/bin/env bash
# Package td-gui for Windows (amd64): build the CGO-free WebView2 binary
# with an embedded icon and version info, then wrap it in an NSIS
# installer. Requires makensis (CI installs it with `choco install nsis`).
#
# Usage: VERSION=1.2.3 build/windows/package.sh   (version without leading v)
set -euo pipefail

cd "$(dirname "$0")/../.."

VERSION="${VERSION:?set VERSION to the release version without a leading v}"
WAILS3="${WAILS3:-dist/bin/wails3}"
OUT=dist/gui
WORK=dist/gui-windows-work

# Chocolatey installs NSIS under Program Files but the current GitHub
# Actions step shell does not inherit the updated machine PATH, so fall
# back to the default install location before giving up.
if ! command -v makensis >/dev/null 2>&1; then
	for d in "/c/Program Files (x86)/NSIS" "/c/Program Files/NSIS"; do
		if [ -x "$d/makensis.exe" ]; then
			PATH="$d:$PATH"
			break
		fi
	done
fi
if ! command -v makensis >/dev/null 2>&1; then
	echo "package.sh: makensis not found; install NSIS (choco install nsis)" >&2
	exit 1
fi

mkdir -p "$OUT" "$WORK"

# Icon and version info are embedded by the Go linker from a .syso file,
# which must sit next to the main package at build time. It is removed
# again when this script exits so the tree stays clean.
"$WAILS3" generate icons -input assets/icon.png \
	-windowsfilename "$WORK/icon.ico" -macfilename "$WORK/icon.icns"
sed "s/@VERSION@/$VERSION/g" build/windows/info.json >"$WORK/info.json"
"$WAILS3" generate syso -arch amd64 \
	-icon "$WORK/icon.ico" \
	-manifest build/windows/wails.exe.manifest \
	-info "$WORK/info.json" \
	-out cmd/td-gui/td-gui_windows_amd64.syso
trap 'rm -f cmd/td-gui/*.syso' EXIT

# The evergreen WebView2 bootstrapper is embedded in the wails3 CLI; the
# installer runs it only when the runtime is missing. This must run BEFORE
# the go build: the generator cleans the output directory first.
"$WAILS3" generate webview2bootstrapper -dir "$WORK"

# Windows WebView2 needs no cgo; windowsgui hides the console window.
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	go build -tags gui -trimpath -ldflags "-s -w -H windowsgui" \
	-o "$WORK/td-gui.exe" ./cmd/td-gui

makensis -NOCD \
	-DVERSION="$VERSION" \
	-DBINARY="$WORK/td-gui.exe" \
	-DBOOTSTRAPPER="$WORK/MicrosoftEdgeWebview2Setup.exe" \
	-DOUTFILE="$OUT/td-gui_${VERSION}_windows_x86_64-installer.exe" \
	build/windows/td-gui.nsi

cd "$OUT"
sha256sum "td-gui_${VERSION}_windows_x86_64-installer.exe" \
	>"td-gui_${VERSION}_windows_x86_64-installer.exe.sha256"
ls -l
