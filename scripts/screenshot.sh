#!/usr/bin/env bash
# Screenshot a surface of the running app.
#
#   scripts/screenshot.sh welcome shots/welcome.png [1100x760]
#
# It works by running Astral under XWayland, where a window can be addressed
# by name, and ASTRAL_DEV_TITLE pins a name no other window will have. That
# matters twice over: an external tool has to find the right window, and it
# must not find the copy you have open and screenshot your actual chats.
#
# The obvious approach, having the app render itself to a texture, does not
# work. gsk_renderer_render_texture in this gotk4 version passes the render
# node through InternObject, which assumes a GObject; GskRenderNode is not
# one, so GSK receives a bogus pointer and returns NULL for any node at all,
# including a trivially valid one. That is a binding limitation, not something
# the app can work around.
#
# Requires ImageMagick for `import`. Data lives in a temporary directory, so
# captures never touch the real database.
set -euo pipefail

view="${1:?usage: screenshot.sh <view> <out.png> [WxH]}"
out="${2:?usage: screenshot.sh <view> <out.png> [WxH]}"
size="${3:-1100x760}"
title="Astral Dev Capture"

command -v import >/dev/null || { echo "ImageMagick is needed for 'import'" >&2; exit 1; }

cd "$(dirname "$(readlink -f "$0")")/.."
[ -x bin/astral ] || make build

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

XDG_DATA_HOME="$tmp" GDK_BACKEND=x11 ASTRAL_DEV_TITLE=1 \
  ASTRAL_DEV_SIZE="$size" ASTRAL_DEV_VIEW="$view" \
  ./bin/astral >/dev/null 2>&1 &
pid=$!
trap 'kill $pid 2>/dev/null || true; rm -rf "$tmp"' EXIT

# Long enough for the window to map and for the delayed dev view to open.
sleep 4
mkdir -p "$(dirname "$out")"
import -window "$title" "$out"
echo "wrote $out"
