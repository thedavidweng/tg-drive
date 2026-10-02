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

cleanup() {
  [ -n "$PID" ] && kill "$PID" 2>/dev/null || true
  [ -n "$SETUP_PID" ] && kill "$SETUP_PID" 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

fail() {
  echo "run.sh: $*" >&2
  [ -f "$SERVER_LOG" ] && { echo "--- td-gui server log ---" >&2; cat "$SERVER_LOG" >&2; }
  [ -f "$SETUP_LOG" ] && { echo "--- td-gui setup-server log ---" >&2; cat "$SETUP_LOG" >&2; }
  exit 1
}

# Build the PR's code: the frontend (embedded by the gui-tagged build), the
# CLI (seeds the drive), and td-gui in server mode (CGO-free: the server
# tag excludes the webview, so no GTK/WebKitGTK is needed).
cd "$ROOT"
(cd frontend && bun install --frozen-lockfile && bun run build)
go build -trimpath -o "$WORK/td" ./cmd/td
CGO_ENABLED=0 go build -tags gui,server -trimpath -o "$WORK/td-gui" ./cmd/td-gui

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
printf 'roadmap: preview, auth, transfers\n' > "$WORK/files/Documents/roadmap.md"
printf 'placeholder bytes for a photo\n' > "$WORK/files/Photos/kyoto.jpg"
printf 'placeholder bytes for a photo\n' > "$WORK/files/Photos/taipei.jpg"

echo "12345" | "$WORK/td" auth login > /dev/null
"$WORK/td" init "$STATE/root" --create-channel=Drive > /dev/null
"$WORK/td" cp "$WORK/files/notes.txt" /notes.txt > /dev/null
"$WORK/td" cp "$WORK/files/Documents/report-q3.md" /Documents/report-q3.md > /dev/null
"$WORK/td" cp "$WORK/files/Documents/roadmap.md" /Documents/roadmap.md > /dev/null
"$WORK/td" cp "$WORK/files/Photos/kyoto.jpg" /Photos/kyoto.jpg > /dev/null
"$WORK/td" cp "$WORK/files/Photos/taipei.jpg" /Photos/taipei.jpg > /dev/null

# Serve the GUI. WAILS_SERVER_PORT=0 would need log parsing, so find a free
# port first; the race is acceptable for a CI job and a local run.
PORT=${TD_PREVIEW_PORT:-$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{console.log(s.address().port);s.close()})')}
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

# Walk the scenes and normalise the recording to mp4 (Playwright writes
# webm; Pages visitors get h264).
PREVIEW_SHA=${PREVIEW_SHA:-$(git rev-parse HEAD)} node "$ROOT/ui-preview/record.mjs" \
  --url "$BASE" --setup-url "$BASE_SETUP" --out "$OUT" \
  || fail "scene recording failed"
WEBM=$(node -pe 'JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).video' "$OUT/manifest.json")
ffmpeg -y -loglevel error -i "$OUT/$WEBM" -c:v libx264 -pix_fmt yuv420p -movflags +faststart "$OUT/preview.mp4"
rm "$OUT/$WEBM"
node -e 'const f = process.argv[1], m = JSON.parse(require("fs").readFileSync(f, "utf8")); m.video = "preview.mp4"; require("fs").writeFileSync(f, JSON.stringify(m, null, 2) + "\n")' "$OUT/manifest.json"

echo "preview written to $OUT"
