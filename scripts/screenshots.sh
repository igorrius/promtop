#!/usr/bin/env bash
# Regenerates docs/*.png and docs/promtop.gif from the bundled demo: terminal
# frames, the web dashboard and a promtop render snapshot.
# Needs Google Chrome (or Chromium) and ffmpeg.
set -euo pipefail
cd "$(dirname "$0")/.."

chrome=${CHROME:-$(command -v google-chrome || command -v chromium || command -v chromium-browser)}
work=$(mktemp -d)
trap 'kill "$demo" 2>/dev/null || true; rm -rf "$work"' EXIT

go build -o "$work/demo" ./examples/demo
"$work/demo" -addr 127.0.0.1:18080 >/dev/null 2>&1 &
demo=$!
until curl -sf http://127.0.0.1:18080/metrics >/dev/null; do sleep 0.2; done

# promtop render itself, sampling while the terminal frames are made.
mkdir -p docs
go build -o "$work/promtop" .
"$work/promtop" render -for 40s -i 1s -title "Demo service" \
	-m 'demo_http_request_duration_seconds' -m 'demo_dependency_up' -m 'demo_circuit_breaker' \
	-m 'demo_db_pool_*' -m 'demo_disk_*' -m 'demo_session_store_bytes' \
	-o docs/render.png 127.0.0.1:18080 &
render=$!

PROMTOP_SHOTS="$work/frames" PROMTOP_SHOTS_TARGET=127.0.0.1:18080 \
	go test ./internal/ui -run Screenshots -count=1

# 120×38 cells of 15px DejaVu Sans Mono (9.03px wide, 17px lines), plus the
# window chrome from framePage.
w=$((1084 + 2 * 18 + 2 * 28 + 2))
h=$((38 * 17 + 34 + 18 + 2 * 28 + 2))
shot() {
	"$chrome" --headless=new --disable-gpu --no-sandbox --hide-scrollbars \
		--force-device-scale-factor="$3" --window-size="$w,$h" \
		--screenshot="$2" "file://$1" >/dev/null 2>&1
}

for f in grid detail-histogram detail-usage help; do
	shot "$work/frames/$f.html" "docs/$f.png" 2
done
for f in "$work"/frames/anim-*.html; do
	shot "$f" "${f%.html}.png" 1
done
wait "$render"

# The web dashboard, after some history has built up.
BROWSER=true "$work/promtop" -web -web-addr 127.0.0.1:18090 -i 1s 127.0.0.1:18080 2>/dev/null &
web=$!
trap 'kill "$demo" "$web" 2>/dev/null || true; rm -rf "$work"' EXIT
until curl -sf http://127.0.0.1:18090/ >/dev/null; do sleep 0.2; done
timeout 60 curl -sN http://127.0.0.1:18090/api/events >/dev/null || true
web() {
	"$chrome" --headless=new --disable-gpu --no-sandbox --hide-scrollbars --force-dark-mode \
		--window-size="$2" --screenshot="$3" "http://127.0.0.1:18090/$1" >/dev/null 2>&1
}
web "" 1440,1350 docs/web.png
web "#metric=demo_http_request_duration_seconds" 1440,1000 docs/web-detail.png

ffmpeg -loglevel error -y -framerate 1.6 -i "$work/frames/anim-%03d.png" \
	-vf "split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=none" \
	docs/promtop.gif
ls -lh docs
