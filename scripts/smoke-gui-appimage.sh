#!/usr/bin/env bash
set -euo pipefail

image="$(realpath "${1:?usage: smoke-gui-appimage.sh IMAGE}")"
if [ "$(id -u)" -eq 0 ]; then
	echo 'Run the td-gui AppImage smoke check as a non-root user' >&2
	exit 1
fi
if [ ! -r "$image" ] || [ ! -x "$image" ]; then
	echo "AppImage is not readable and executable: $image" >&2
	exit 1
fi
python3 - "$image" <<'PY'
import stat
import sys
from pathlib import Path

mode = stat.S_IMODE(Path(sys.argv[1]).stat().st_mode)
if mode & 0o555 != 0o555 or mode & 0o444 != 0o444:
    raise SystemExit(f'AppImage has non-public mode {mode:04o}; needs read/execute for all users')
PY
case "${image##*/}" in
	*_linux_x86_64.AppImage) appimage_arch=x86_64 ;;
	*_linux_aarch64.AppImage) appimage_arch=aarch64 ;;
	*) echo "unsupported td-gui AppImage filename: ${image##*/}" >&2; exit 1 ;;
esac
zsync="$image.zsync"
if [ ! -s "$zsync" ]; then
	echo "AppImage zsync sidecar is missing or empty: $zsync" >&2
	exit 1
fi
if ! update_info="$(env -u APPIMAGE_EXTRACT_AND_RUN "$image" --appimage-updateinformation 2>&1)"; then
	printf '%s\n' "$update_info" >&2
	echo 'AppImage runtime did not return update information' >&2
	exit 1
fi
printf '%s\n' "$update_info" | grep -Fq "gh-releases-zsync|thedavidweng|tg-drive|latest|td-gui_*_linux_${appimage_arch}.AppImage.zsync"

offset="$(env -u APPIMAGE_EXTRACT_AND_RUN "$image" --appimage-offset)"
work="$(mktemp -d)"
app_pid=''
trap 'if [ -n "$app_pid" ]; then sudo -n kill -- "-$app_pid" 2>/dev/null || true; wait "$app_pid" 2>/dev/null || true; fi; rm -rf "$work"' EXIT

cd "$work"
unsquashfs -offset "$offset" -d squashfs-root "$image" >/dev/null
desktop-file-validate "$work/squashfs-root/td-gui.desktop"
python3 - "$work/squashfs-root" <<'PY'
import stat
import sys
from pathlib import Path
root = Path(sys.argv[1])
required = {
    '.': 0o555,
    'AppRun': 0o555,
    'usr': 0o111,
    'usr/bin': 0o111,
    'usr/bin/td-gui': 0o555,
    'td-gui.desktop': 0o444,
    'td-gui.png': 0o444,
}
for relative, bits in required.items():
    path = root / relative
    if not path.exists():
        raise SystemExit(f'missing AppImage entry: {relative}')
    mode = stat.S_IMODE(path.stat().st_mode)
    if mode & bits != bits:
        raise SystemExit(f'AppImage entry {relative} has mode {mode:04o}; needs {bits:04o}')

desktop = (root / 'td-gui.desktop').read_text(encoding='utf-8')
if 'Exec=td-gui' not in desktop or 'Icon=td-gui' not in desktop:
    raise SystemExit('desktop metadata does not identify td-gui')
PY

mkdir -p "$work/home/config" "$work/home/cache" "$work/home/data"
setsid sudo -n unshare --net -- runuser -u "$(id -un)" -- \
	env DISPLAY="$DISPLAY" XAUTHORITY="${XAUTHORITY:-}" HOME="$work/home" \
	XDG_CONFIG_HOME="$work/home/config" XDG_CACHE_HOME="$work/home/cache" XDG_DATA_HOME="$work/home/data" LIBGL_ALWAYS_SOFTWARE=1 \
	"$work/squashfs-root/AppRun" >app.log 2>&1 &
app_pid=$!
for _ in $(seq 1 30); do
	if ! kill -0 "$app_pid" 2>/dev/null; then
		cat app.log
		echo 'td-gui AppImage exited before displaying a window' >&2
		exit 1
	fi
	gui_pid="$(pgrep -n -u "$(id -u)" -x td-gui || true)"
	if [ -n "$gui_pid" ] && xdotool search --onlyvisible --pid "$gui_pid" >windows.txt 2>/dev/null; then
		sleep 3
		kill -0 "$app_pid"
		import -window "$(cat windows.txt)" "${RUNNER_TEMP:-$work}/td-gui-appimage-smoke.png"
		cat app.log
		echo 'td-gui AppImage passed metadata, permission, offline, non-root, and GUI startup checks'
		exit 0
	fi
	sleep 1
done
cat app.log
echo 'td-gui AppImage did not display a window within 30 seconds' >&2
exit 1
