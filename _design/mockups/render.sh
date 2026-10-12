#!/usr/bin/env bash
# render.sh [name...]: screenshot src/<name>.html to <name>.png (all when none given)
set -uo pipefail
cd "$(dirname "$0")"
names=("$@"); [ ${#names[@]} -eq 0 ] && names=($(cd src && ls *.html | sed 's/\.html$//'))
for n in "${names[@]}"; do
  h=$(grep -o 'data-h="[0-9]*"' "src/$n.html" | head -1 | grep -o '[0-9]*' || true); h=${h:-1000}
  setsid timeout 40 chromium-headless-shell --no-sandbox --disable-gpu --hide-scrollbars \
    --force-device-scale-factor=1 --window-size=1600,$h --virtual-time-budget=1500 \
    --user-data-dir="$(mktemp -d)" --screenshot="$PWD/$n.png" "file://$PWD/src/$n.html" >/dev/null 2>&1 &
  pid=$!; wait $pid || echo "fail $n"
  kill -- -$pid 2>/dev/null || true
done
