#!/usr/bin/env bash
# Package td-gui for the host Linux architecture: a .deb (nfpm, via the
# pinned wails3 CLI) and an AppImage (linuxdeploy with the GTK plugin, via
# wails3). Builds use Wails' GTK3/WebKitGTK 4.1 backend on Ubuntu 22.04,
# keeping the AppImage at the glibc 2.35 compatibility baseline.
#
# Usage: VERSION=1.2.3 build/linux/package.sh   (version without leading v)
# Requires the GTK3/WebKitGTK 4.1 development packages and network access: the
# AppImage step downloads linuxdeploy and AppRun from their upstream releases.
set -euo pipefail

cd "$(dirname "$0")/../.."

VERSION="${VERSION:?set VERSION to the release version without a leading v}"
WAILS3="${WAILS3:-dist/bin/wails3}"
OUT=dist/gui
WORK=dist/gui-linux-work
. build/version.sh

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

rm -rf "$WORK"
mkdir -p "$OUT" "$WORK"

# Same build flags as `make gui-build`, plus stripping for release.
CGO_ENABLED=1 go build -tags gui,gtk3 -trimpath -ldflags "-s -w $VERSION_LDFLAGS" -o dist/td-gui ./cmd/td-gui
verify_version dist/td-gui

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
export UPDATE_INFORMATION="gh-releases-zsync|thedavidweng|tg-drive|latest|td-gui_*_linux_${APPIMAGE_ARCH}.AppImage.zsync"
"$WAILS3" generate appimage \
	-binary dist/td-gui \
	-icon "$WORK/td-gui.png" \
	-desktopfile build/linux/td-gui.desktop \
	-outputdir "$WORK" \
	-builddir "$WORK/appimage"
artifact="$OUT/td-gui_${VERSION}_linux_${APPIMAGE_ARCH}.AppImage"
mv "$WORK/td-gui-${APPIMAGE_ARCH}.AppImage" "$artifact"
zsync="$artifact.zsync"
if [ ! -s "$zsync" ]; then
	found="$(find "$WORK" . -maxdepth 3 -name '*.AppImage.zsync' -print -quit 2>/dev/null || true)"
	if [ -n "$found" ]; then mv "$found" "$zsync"; fi
fi
if [ ! -s "$zsync" ]; then
	command -v zsyncmake >/dev/null 2>&1 || {
		echo "package.sh: no .zsync was produced and zsyncmake is unavailable; install zsync" >&2
		exit 1
	}
	zsyncmake -u "$(basename "$artifact")" -o "$zsync" "$artifact"
fi
test -s "$zsync"

cd "$OUT"
for artifact in \
	"td-gui_${VERSION}_linux_${DEBARCH}.deb" \
	"td-gui_${VERSION}_linux_${APPIMAGE_ARCH}.AppImage" \
	"td-gui_${VERSION}_linux_${APPIMAGE_ARCH}.AppImage.zsync"; do
	sha256sum "$artifact" >"$artifact.sha256"
done
ls -l
