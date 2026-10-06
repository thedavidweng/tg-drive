#!/usr/bin/env bash
# Records the td-gui UI preview: seed a fake-Telegram drive through the real
# CLI, serve the real GUI facade with a server-mode td-gui build, then walk
# the scenes in record.mjs with Playwright. The output directory gets one
# 2x PNG per scene, preview.mp4, and manifest.json for publish.mjs.
#
#   ui-preview/run.sh <out-dir>
#
# Needs go, bun, node, and ffmpeg on PATH (mise provides the first three).
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
OUT=$(mkdir -p "$1" && cd "$1" && pwd)
WORK=$(mktemp -d)
SERVER_LOG=$WORK/server.log
SETUP_LOG=$WORK/server-setup.log
PID=""
SETUP_PID=""
OMARCHY_PID=""
OMARCHY_LOG=$WORK/server-omarchy.log

cleanup() {
  [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  [ -n "$SETUP_PID" ] && kill "$SETUP_PID" 2>/dev/null || true
  [ -n "$OMARCHY_PID" ] && kill "$OMARCHY_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
  echo "run.sh: $*" >&2
  [ -f "$SERVER_LOG" ] && { echo "--- td-gui server log ---" >&2; cat "$SERVER_LOG" >&2; }
  [ -f "$SETUP_LOG" ] && { echo "--- td-gui setup-server log ---" >&2; cat "$SETUP_LOG" >&2; }
  [ -f "$OMARCHY_LOG" ] && { echo "--- td-gui omarchy-server log ---" >&2; cat "$OMARCHY_LOG" >&2; }
  exit 1
}

# Build the PR's code: the frontend (embedded by the gui-tagged build), the
# CLI (seeds the drive), and td-gui in server mode (CGO-free: the server
# tag excludes the webview, so no GTK/WebKitGTK is needed).
cd "$ROOT"
(cd frontend && bun install --frozen-lockfile && bun run build)
go build -trimpath -o "$WORK/td" ./cmd/td
CGO_ENABLED=0 go build -tags gui,server -trimpath -o "$WORK/td-gui" ./cmd/td-gui
export TD_PREVIEW_CLI_VERSION=$("$WORK/td" version --json | node -e 'let s="";process.stdin.on("data",b=>s+=b).on("end",()=>console.log(JSON.parse(s).data.version))')

# Seed the fake Telegram through the CLI, the same flow the binary E2E
# tests use, so the GUI opens a drive it did not write.
STATE=$WORK/state
mkdir -p "$STATE/root" "$WORK/files/Documents" "$WORK/files/Photos"
cat > "$STATE/config.toml" <<'EOF'
[telegram]
api_id = 12345
api_hash = "deadbeef"
phone = "+15551234567"
EOF
# Exported, not per-command: the td-gui server must open the same config,
# database, and fake state the CLI seeds below, never the developer's real
# defaults. TD_OMARCHY=0 keeps the preview identical on every machine: on
# an Omarchy desktop the detected theme would otherwise apply and hide the
# header's theme toggle the walkthrough video clicks.
export TD_FAKE_TELEGRAM=1 TD_FAKE_TELEGRAM_STATE="$STATE/fake.json" \
  TD_CONFIG="$STATE/config.toml" TD_DB="$STATE/td.db" TD_SESSION="$STATE/session.json" \
  TD_OMARCHY=0

printf 'chapters outline and open questions\n' > "$WORK/files/notes.txt"
printf 'Q3 report draft\n' > "$WORK/files/Documents/report-q3.md"
printf '# Preview roadmap\n\nInspect **remote files** without downloading them.\n' > "$WORK/files/Documents/roadmap.md"
node -e 'const fs = require("fs"), dir = process.argv[1]; fs.writeFileSync(dir + "/large.log", "Initial log chunk\n" + " ".repeat(2 * 1024 * 1024) + "\nFinal log chunk\n"); fs.writeFileSync(dir + "/archive.zip", Buffer.from([0x50, 0x4b, 0, 0]));' "$WORK/files/Documents"
# Real JPEGs, drawn deterministically: the image preview scene opens one
# and waits for the browser to decode it.
mkdir -p "$WORK/files/Photos/2024"
go run ui-preview/photo.go "$WORK/files/Photos/kyoto.jpg" dusk
go run ui-preview/photo.go "$WORK/files/Photos/taipei.jpg" night
go run ui-preview/photo.go "$WORK/files/Photos/2024/alley.jpg" dawn

echo "12345" | "$WORK/td" auth login > /dev/null
"$WORK/td" init "$STATE/root" --create-channel=Drive > /dev/null
"$WORK/td" cp "$WORK/files/notes.txt" /notes.txt > /dev/null
"$WORK/td" cp "$WORK/files/Documents/report-q3.md" /Documents/report-q3.md > /dev/null
"$WORK/td" cp "$WORK/files/Documents/roadmap.md" /Documents/roadmap.md > /dev/null
"$WORK/td" cp "$WORK/files/Documents/large.log" /Documents/large.log > /dev/null
"$WORK/td" cp "$WORK/files/Documents/archive.zip" /Documents/archive.zip > /dev/null
"$WORK/td" cp "$WORK/files/Photos/kyoto.jpg" /Photos/kyoto.jpg > /dev/null
"$WORK/td" cp "$WORK/files/Photos/taipei.jpg" /Photos/taipei.jpg > /dev/null
"$WORK/td" cp "$WORK/files/Photos/2024/alley.jpg" /Photos/2024/alley.jpg > /dev/null

# Two more channels for the switcher scenes. Backups is bound to a second
# root in the same database, so the switcher offers a real switch target;
# it holds one file so the switched view is not empty. Photos Archive is
# created against a scratch database: the fake Telegram account owns it,
# but the preview's database never binds it, so the bind sheet offers it.
mkdir -p "$STATE/root-backups" "$WORK/scratch-root"
printf 'nightly backup archive\n' > "$WORK/files/backup.txt"
"$WORK/td" init "$STATE/root-backups" --create-channel=Backups > /dev/null
"$WORK/td" cp "$WORK/files/backup.txt" /backup.txt --channel=Backups > /dev/null
TD_DB="$WORK/scratch.db" "$WORK/td" init "$WORK/scratch-root" --create-channel="Photos Archive" > /dev/null

# The Transfers scenes need a drive with history. A 12 MiB file crosses
# the fake's resumable threshold, so uploads of it span multiple parts.
head -c 12582912 /dev/zero > "$WORK/files/big.bin"
# One failed Transfer in the history: the fake fails the first resumable
# upload after two confirmed parts, and the row keeps the error code.
TD_FAKE_FAIL_UPLOAD_AFTER_PARTS=2 \
  "$WORK/td" cp "$WORK/files/big.bin" /broken.bin --upload-part-size-kb 1024 --upload-threads 1 > /dev/null 2>&1 || true
# The owner's cancel poll is a third of locks.ttl_seconds; shorten it so
# the GUI's cancel of a CLI upload turns around in seconds, not minutes.
"$WORK/td" config set locks.ttl_seconds 3

# The scripted picker answers: native dialogs are no-ops in server mode,
# so td-gui falls back to these. Two 22 MiB photos keep the live-upload
# scene's album in the uploading stage for several seconds.
mkdir -p "$WORK/pick" "$WORK/downloads"
head -c 23068672 /dev/zero > "$WORK/pick/picnic.jpg"
head -c 23068672 /dev/zero > "$WORK/pick/sunset.jpg"
PICK_FILES="$WORK/pick/picnic.jpg:$WORK/pick/sunset.jpg"
# The CLI uploads the Transfers scenes start mid-recording, against the
# same fake Telegram; one thread and a slow part keep them cancellable.
# (The fake serializes Telegram calls, so these run only while no scene is
# loading a page.)
export TD_PREVIEW_CLI_CP="TD_FAKE_TRANSFER_DELAY=1s '$WORK/td' cp '$WORK/files/big.bin' /big.bin --upload-part-size-kb 1024 --upload-threads 1"
export TD_PREVIEW_CLI_CP2="TD_FAKE_TRANSFER_DELAY=1s '$WORK/td' cp '$WORK/files/big.bin' /cli-slow.bin --upload-part-size-kb 1024 --upload-threads 1"

# Saved Messages and one unmanaged channel post for the Import and
# Maintenance scenes — the CLI has no commands for either, so seed the
# fake state directly.
go run ui-preview/seed.go "$STATE/fake.json"

# The Omarchy server (below) gets its own copy of the seeded drive: two
# td-gui processes cannot share one Telegram session.
cp -R "$STATE" "$WORK/state-omarchy"

# Serve the GUI. WAILS_SERVER_PORT=0 would need log parsing, so find a free
# port first; the race is acceptable for a CI job and a local run.
PORT=${TD_PREVIEW_PORT:-$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}
# PATH carries the preview's td so the Settings About row probes it.
PATH="$WORK:$PATH" TD_FAKE_TRANSFER_DELAY=800ms TD_FAKE_PART_SIZE=1048576 \
  TD_GUI_PICK_FILES="$PICK_FILES" TD_GUI_PICK_DIR="$WORK/downloads" \
  WAILS_SERVER_HOST=127.0.0.1 WAILS_SERVER_PORT=$PORT "$WORK/td-gui" > "$SERVER_LOG" 2>&1 &
PID=$!
BASE=http://127.0.0.1:$PORT
for _ in $(seq 100); do
  curl -sf -o /dev/null "$BASE/" && break
  kill -0 "$PID" 2>/dev/null || fail "td-gui exited before serving"
  sleep 0.2
done
curl -sf -o /dev/null "$BASE/" || fail "td-gui did not serve on $BASE"

# The setup and login scenes need a fresh machine: a second td-gui whose
# config, database, session, and fake state hold no credentials, so the
# first run starts on the setup form. Its fake account has two-step
# verification, so the login scenes answer a password after the code.
# TD_FAKE_AUTH_PASSWORD is per-command on purpose: it must not leak into
# the seeded drive above.
STATE_SETUP=$WORK/state-setup
mkdir -p "$STATE_SETUP"
SETUP_PORT=${TD_PREVIEW_SETUP_PORT:-$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}
TD_CONFIG="$STATE_SETUP/config.toml" TD_DB="$STATE_SETUP/td.db" TD_SESSION="$STATE_SETUP/session.json" \
  TD_FAKE_TELEGRAM_STATE="$STATE_SETUP/fake.json" TD_FAKE_AUTH_PASSWORD="hunter2" \
  WAILS_SERVER_HOST=127.0.0.1 WAILS_SERVER_PORT=$SETUP_PORT "$WORK/td-gui" > "$SETUP_LOG" 2>&1 &
SETUP_PID=$!
BASE_SETUP=http://127.0.0.1:$SETUP_PORT
for _ in $(seq 100); do
  curl -sf -o /dev/null "$BASE_SETUP/" && break
  kill -0 "$SETUP_PID" 2>/dev/null || fail "td-gui (setup) exited before serving"
  sleep 0.2
done
curl -sf -o /dev/null "$BASE_SETUP/" || fail "td-gui (setup) did not serve on $BASE_SETUP"

# The Omarchy scenes need a desktop that has Omarchy: a third td-gui on a
# copy of the seeded drive, with detection forced on and pointed at a seeded
# current theme (the layout Omarchy 4 keeps under ~/.local/state) and a
# Hyprland config that rounds windows. The scenes rewrite the theme to show
# a live change, so the fast poll keeps that wait short.
OMARCHY_CURRENT=$WORK/omarchy/state/omarchy/current
mkdir -p "$OMARCHY_CURRENT/theme" "$WORK/omarchy/config/hypr"
printf 'mode = "dark"\nbackground = "#1a1b26"\nforeground = "#c0caf5"\naccent = "#7aa2f7"\n' \
  > "$OMARCHY_CURRENT/theme/colors.toml"
echo preview-night > "$OMARCHY_CURRENT/theme.name"
printf 'general {\n    border_size = 2\n}\ndecoration {\n    rounding = 8\n}\n' > "$WORK/omarchy/config/hypr/hyprland.conf"
export TD_PREVIEW_OMARCHY_THEME="$OMARCHY_CURRENT/theme"
OMARCHY_PORT=${TD_PREVIEW_OMARCHY_PORT:-$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}
STATE_OMARCHY=$WORK/state-omarchy
TD_CONFIG="$STATE_OMARCHY/config.toml" TD_DB="$STATE_OMARCHY/td.db" TD_SESSION="$STATE_OMARCHY/session.json" \
  TD_FAKE_TELEGRAM_STATE="$STATE_OMARCHY/fake.json" \
  PATH="$WORK:$PATH" TD_OMARCHY=1 TD_OMARCHY_THEME="$TD_PREVIEW_OMARCHY_THEME" TD_OMARCHY_POLL=200ms \
  XDG_CONFIG_HOME="$WORK/omarchy/config" \
  WAILS_SERVER_HOST=127.0.0.1 WAILS_SERVER_PORT=$OMARCHY_PORT "$WORK/td-gui" > "$OMARCHY_LOG" 2>&1 &
OMARCHY_PID=$!
BASE_OMARCHY=http://127.0.0.1:$OMARCHY_PORT
for _ in $(seq 100); do
  curl -sf -o /dev/null "$BASE_OMARCHY/" && break
  kill -0 "$OMARCHY_PID" 2>/dev/null || fail "td-gui (omarchy) exited before serving"
  sleep 0.2
done
curl -sf -o /dev/null "$BASE_OMARCHY/" || fail "td-gui (omarchy) did not serve on $BASE_OMARCHY"

# Walk the scenes and normalise the recording to mp4 (Playwright writes
# webm; Pages visitors get h264).
PREVIEW_SHA=${PREVIEW_SHA:-$(git rev-parse HEAD)} node "$ROOT/ui-preview/record.mjs" \
  --url "$BASE" --setup-url "$BASE_SETUP" --omarchy-url "$BASE_OMARCHY" --out "$OUT" \
  || fail "scene recording failed"
WEBM=$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).video' "$OUT/manifest.json")
ffmpeg -y -loglevel error -i "$OUT/$WEBM" -c:v libx264 -pix_fmt yuv420p -movflags +faststart "$OUT/preview.mp4"
rm "$OUT/$WEBM"
node -e 'const f = process.argv[1], m = JSON.parse(require("fs").readFileSync(f, "utf8")); m.video = "preview.mp4"; require("fs").writeFileSync(f, JSON.stringify(m, null, 2) + "\n")' "$OUT/manifest.json"

echo "preview written to $OUT"
