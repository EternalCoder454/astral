#!/usr/bin/env bash
# Regenerate every screenshot in screenshots/.
#
# The images in that folder are committed, so the README and the repository
# page have something to show. This script is what remakes them, so they can be
# brought back in line after an interface change rather than going stale and
# quietly misrepresenting the app.
#
# It seeds a throwaway database with a world, a cast and a played scene first.
# Empty states are the easiest thing in an interface to get right and the least
# informative to look at, and at least one sizing bug here only appeared once
# there was real content to size.
#
# Requires ImageMagick. Nothing touches the real database or settings: both are
# temporary directories, and the config is a copy.
set -euo pipefail

cd "$(dirname "$(readlink -f "$0")")/.."
command -v import >/dev/null || { echo "ImageMagick is needed for 'import'" >&2; exit 1; }
command -v magick  >/dev/null || { echo "ImageMagick is needed for 'magick'" >&2; exit 1; }

size="${ASTRAL_SHOT_SIZE:-1240x820}"
title="Astral Dev Capture"
out=screenshots
mkdir -p "$out"

make build

seed=$(mktemp -d)
cfg=$(mktemp -d)
trap 'rm -rf "$seed" "$cfg"' EXIT

# A copy of the real settings, so the theme and persona match what a person
# actually sees, with the writes landing in the copy.
if [ -f "${XDG_CONFIG_HOME:-$HOME/.config}/astral/config.json" ]; then
    mkdir -p "$cfg/astral"
    cp "${XDG_CONFIG_HOME:-$HOME/.config}/astral/config.json" "$cfg/astral/config.json"
    # Settings that differ from the defaults are forced back for the capture.
    # These images stand in for the app as someone installing it today meets
    # it, not for one developer's config: generation statistics under every
    # reply are off by default, and leaving them on here would misrepresent
    # the thing being reviewed. The copy is a throwaway, the real config is
    # untouched.
    python3 -c 'import json,sys; p=sys.argv[1]; c=json.load(open(p)); c["show_stats"]=False; json.dump(c, open(p,"w"), indent=2)' \
        "$cfg/astral/config.json"
fi

echo "seeding..."
XDG_DATA_HOME="$seed" XDG_CONFIG_HOME="$cfg" GDK_BACKEND=x11 \
    ASTRAL_DEV_TITLE=1 ASTRAL_DEV_SEED=1 ASTRAL_DEV_SIZE=900x600 \
    timeout 10 ./bin/astral >/dev/null 2>&1 || true

grab() {
    local view="$1" name="$2"
    local d c
    d=$(mktemp -d); c=$(mktemp -d)
    cp -r "$seed/." "$d/" 2>/dev/null || true
    cp -r "$cfg/."  "$c/" 2>/dev/null || true

    env XDG_DATA_HOME="$d" XDG_CONFIG_HOME="$c" GDK_BACKEND=x11 \
        ASTRAL_DEV_TITLE=1 ASTRAL_DEV_SIZE="$size" \
        ${view:+ASTRAL_DEV_VIEW="$view"} \
        ./bin/astral >/dev/null 2>&1 &
    local pid=$!
    # Long enough for the window to map and the delayed dev view to open.
    sleep 5
    import -window "$title" "$out/$name.png" 2>/dev/null || echo "  (failed) $name" >&2
    kill $pid 2>/dev/null || true
    wait $pid 2>/dev/null || true
    magick "$out/$name.png" -strip -define png:compression-level=9 "$out/$name.png" 2>/dev/null || true
    rm -rf "$d" "$c"
    echo "  $name"
}

# "scene" opens the newest chat. The saved last_chat is not used: it points
# into the real database, not the seeded one, and the capture would quietly
# come back as the welcome screen.
grab scene        01-chat
grab portrait     02-portrait
grab welcome      03-welcome
grab newchat      04-new-chat
grab characters   05-characters
grab editchar     06-character-editor
grab worlds       07-worlds
grab world        08-world
grab lorebook     09-lorebook
grab styles       10-writing-styles
grab settings     11-settings

echo "wrote $(ls -1 "$out"/*.png | wc -l) screenshots to $out/"
