#!/usr/bin/env bash
# Regenerates docs/*.png and docs/promtop.gif from the bundled demo.
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

mkdir -p docs
for f in grid detail-histogram detail-usage help; do
	shot "$work/frames/$f.html" "docs/$f.png" 2
done
for f in "$work"/frames/anim-*.html; do
	shot "$f" "${f%.html}.png" 1
done
ffmpeg -loglevel error -y -framerate 1.6 -i "$work/frames/anim-%03d.png" \
	-vf "split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=none" \
	docs/promtop.gif
ls -lh docs
