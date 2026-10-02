#!/usr/bin/env bash
# Package td-gui for the host Linux architecture: a .deb (nfpm, via the
# pinned wails3 CLI) and an AppImage (linuxdeploy with the GTK plugin, via
# wails3). Requires the GTK 4 / WebKitGTK 6.0 dev packages used by
# `make gui-build`, plus network access: the AppImage step downloads
# linuxdeploy and AppRun from their upstream "continuous" releases.
#
# Usage: VERSION=1.2.3 build/linux/package.sh   (version without leading v)
set -euo pipefail

cd "$(dirname "$0")/../.."

VERSION="${VERSION:?set VERSION to the release version without a leading v}"
WAILS3="${WAILS3:-dist/bin/wails3}"
OUT=dist/gui
WORK=dist/gui-linux-work

case "$(go env GOARCH)" in
amd64)
	DEBARCH=amd64
	APPIMAGE_ARCH=x86_64
	;;
arm64)
	DEBARCH=arm64
	APPIMAGE_ARCH=aarch64
	;;
*)
	echo "package.sh: unsupported host architecture $(go env GOARCH)" >&2
	exit 1
	;;
esac

mkdir -p "$OUT" "$WORK"

# Same build flags as `make gui-build`, plus stripping for release.
CGO_ENABLED=1 go build -tags gui -trimpath -ldflags "-s -w" -o dist/td-gui ./cmd/td-gui

# .deb. nfpm expands VERSION and DEBARCH from the environment.
export VERSION DEBARCH
"$WAILS3" tool package -nocolour -name td-gui -format deb \
	-config build/linux/nfpm.yaml -out "$WORK"
mv "$WORK/td-gui.deb" "$OUT/td-gui_${VERSION}_linux_${DEBARCH}.deb"

# AppImage. The desktop file's Icon= must match the icon file basename, so
# the icon travels as td-gui.png. APPIMAGE_EXTRACT_AND_RUN lets the
# AppImage-packaged tooling (linuxdeploy, appimagetool) run on hosts and CI
# runners without FUSE.
cp assets/icon.png "$WORK/td-gui.png"
export APPIMAGE_EXTRACT_AND_RUN=1
"$WAILS3" generate appimage \
	-binary dist/td-gui \
	-icon "$WORK/td-gui.png" \
	-desktopfile build/linux/td-gui.desktop \
	-outputdir "$WORK" \
	-builddir "$WORK/appimage"
mv "$WORK/td-gui-${APPIMAGE_ARCH}.AppImage" "$OUT/td-gui_${VERSION}_linux_${APPIMAGE_ARCH}.AppImage"

cd "$OUT"
for artifact in \
	"td-gui_${VERSION}_linux_${DEBARCH}.deb" \
	"td-gui_${VERSION}_linux_${APPIMAGE_ARCH}.AppImage"; do
	sha256sum "$artifact" >"$artifact.sha256"
done
ls -l
