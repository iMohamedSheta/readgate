#!/usr/bin/env bash
# Screenshot the ReadGate desktop app on macOS / Linux — seeded + stable.
#
# Mirrors scripts/screenshot.ps1 (Windows):
#   1. Fresh isolated profile (READGATE_HOME) seeded with SYNTHETIC demo data
#      via `ReadGate --shot-seed` — never your live profile.
#   2. Launch the app, wait for its window, foreground it, let it settle.
#   3. Capture the window to a PNG.
#
# Capture backends (first available wins):
#   macOS : screencapture -l <window-id> (Quartz, when pyobjc exists),
#           else screencapture -R<x,y,w,h> from the real window bounds,
#           else fullscreen main monitor (-m). All non-interactive — never
#           `-w`, which waits for a mouse click and hangs CI.
#   Linux : gnome-screenshot -w, else import (ImageMagick), else scrot, else grim.
#
# On headless Linux CI, run under Xvfb (the release workflow does this):
#   xvfb-run -a ./scripts/screenshot.sh -Exe build/bin/ReadGate -Out screenshot-linux.png
#
# Usage:
#   ./scripts/screenshot.sh -Exe build/bin/ReadGate -Out screenshot.png
#   ./scripts/screenshot.sh --exe build/bin/ReadGate.app/Contents/MacOS/ReadGate --out docs/screenshot-macos.png
set -euo pipefail

EXE="build/bin/ReadGate"
OUT="screenshot.png"
PROFILE_DIR=""
TIMEOUT_SEC=90
SETTLE_SEC=6
NO_SEED=0

while [ $# -gt 0 ]; do
  case "$1" in
    -Exe|--exe) EXE="$2"; shift 2 ;;
    -Out|--out) OUT="$2"; shift 2 ;;
    -ProfileDir|--profile-dir) PROFILE_DIR="$2"; shift 2 ;;
    -TimeoutSec|--timeout) TIMEOUT_SEC="$2"; shift 2 ;;
    -SettleSec|--settle) SETTLE_SEC="$2"; shift 2 ;;
    -NoSeed|--no-seed) NO_SEED=1; shift ;;
    -h|--help) sed -n '1,30p' "$0"; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

OS="$(uname -s)"
EXE_ABS="$(cd "$(dirname "$EXE")" && pwd)/$(basename "$EXE")"
if [ ! -x "$EXE_ABS" ] && [ ! -f "$EXE_ABS" ]; then
  echo "Executable not found: $EXE ($EXE_ABS)" >&2
  exit 1
fi
chmod +x "$EXE_ABS" 2>/dev/null || true

if [ -z "$PROFILE_DIR" ]; then
  PROFILE_DIR="${TMPDIR:-/tmp}/readgate-shot"
fi
rm -rf "$PROFILE_DIR"
mkdir -p "$PROFILE_DIR"
export READGATE_HOME="$PROFILE_DIR"
# Software rendering for CI / VMs without a GPU.
export WEBKIT_DISABLE_COMPOSITING_MODE=1
export LIBGL_ALWAYS_SOFTWARE=1

if [ "$NO_SEED" -eq 0 ]; then
  echo "Seeding demo profile at $PROFILE_DIR ..."
  "$EXE_ABS" --shot-seed
  echo "Profile contents:"
  find "$PROFILE_DIR" -maxdepth 3 -type f -exec ls -lh {} \;
fi

echo "Launching $EXE_ABS ..."
"$EXE_ABS" &
APP_PID=$!
cleanup() {
  if kill -0 "$APP_PID" 2>/dev/null; then kill "$APP_PID" 2>/dev/null || true; fi
  wait "$APP_PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "Waiting up to ${TIMEOUT_SEC}s for the app window + ${SETTLE_SEC}s settle ..."
# Give the WebView time to boot and render the Fleet view.
sleep "$SETTLE_SEC"
# Extra settle: the first paint is often blank on cold CI runners.
sleep 3
if ! kill -0 "$APP_PID" 2>/dev/null; then
  echo "App exited early." >&2
  exit 1
fi

mkdir -p "$(dirname "$OUT")"
captured=0

if [ "$OS" = "Darwin" ]; then
  # Bring the app forward. System Events works even when the app was launched
  # via its raw binary (which LaunchServices may not know as "ReadGate").
  osascript -e 'tell application "System Events" to set frontmost of (first process whose name contains "ReadGate") to true' 2>/dev/null || \
    osascript -e 'tell application "ReadGate" to activate' 2>/dev/null || true

  # 1) Exact window capture via Quartz window id (needs pyobjc; often absent on CI).
  WINID="$(python3 -c '
import sys
try:
    from Quartz import CGWindowListCopyWindowInfo, kCGWindowListOptionOnScreenOnly, kCGNullWindowID
    for w in CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly, kCGNullWindowID):
        name = str(w.get("kCGWindowOwnerName",""))
        if "ReadGate" in name:
            print(w.get("kCGWindowNumber",""))
            break
except Exception as e:
    sys.stderr.write(str(e))
' 2>/dev/null || true)"
  if [ -n "${WINID:-}" ]; then
    echo "Capturing macOS window $WINID -> $OUT"
    if screencapture -l "$WINID" -x "$OUT"; then captured=1; fi
  fi

  # 2) Region capture from the window's real bounds (non-interactive).
  if [ "$captured" -eq 0 ]; then
    BOUNDS="$(osascript -e 'tell application "System Events" to tell (first process whose name contains "ReadGate") to get {position, size} of window 1' 2>/dev/null | tr -cs '0-9' ' ' || true)"
    # BOUNDS is now "x y w h" (possibly empty).
    # shellcheck disable=SC2086
    set -- $BOUNDS
    if [ $# -ge 4 ] && [ "$3" -ge 800 ] && [ "$4" -ge 500 ]; then
      echo "Capturing macOS window region $1,$2,$3,$4 -> $OUT"
      if screencapture -x -R"$1,$2,$3,$4" "$OUT"; then captured=1; fi
    else
      echo "Could not read ReadGate window bounds (got: '${BOUNDS:-empty}')."
    fi
  fi

  # 3) Last resort: fullscreen main monitor. Non-interactive, never hangs.
  if [ "$captured" -eq 0 ]; then
    echo "Capturing macOS main monitor fullscreen ..."
    if screencapture -x -m "$OUT"; then captured=1; fi
  fi
else
  # Linux: prefer gnome-screenshot -w (active window), then ImageMagick, scrot, grim.
  if command -v gnome-screenshot >/dev/null 2>&1; then
    echo "Capturing with gnome-screenshot -w ..."
    if gnome-screenshot -w -b -f "$OUT"; then captured=1; fi
  fi
  if [ "$captured" -eq 0 ] && command -v import >/dev/null 2>&1; then
    echo "Capturing with ImageMagick import ..."
    sleep 1
    if import -window root "$OUT"; then captured=1; fi
  fi
  if [ "$captured" -eq 0 ] && command -v scrot >/dev/null 2>&1; then
    echo "Capturing with scrot ..."
    if scrot -u "$OUT"; then captured=1; fi
  fi
  if [ "$captured" -eq 0 ] && command -v grim >/dev/null 2>&1; then
    echo "Capturing with grim ..."
    if grim "$OUT"; then captured=1; fi
  fi
fi

if [ "$captured" -eq 0 ] || [ ! -f "$OUT" ]; then
  echo "No capture backend succeeded (tried screencapture/gnome-screenshot/import/scrot/grim)." >&2
  exit 1
fi

SIZE=$(wc -c < "$OUT" | tr -d ' ')
echo "Screenshot saved: $OUT ($SIZE bytes)"
if [ "$SIZE" -lt 20000 ]; then
  echo "WARNING: screenshot suspiciously small — window may not have rendered." >&2
fi
