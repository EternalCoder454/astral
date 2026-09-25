#!/usr/bin/env bash
# Collect everything astral.exe needs to run on a machine without MSYS2.
#
# A GTK application is not one file. It needs its own DLLs and every DLL those
# pull in, the GSettings schemas GTK reads at startup, the pixbuf loaders that
# decode PNGs, and an icon theme for the stock icons the window controls use.
# Miss any of them and it either refuses to start or starts and renders
# nothing, which is a bad way to find out.
set -euo pipefail

out="${1:-dist}"
mingw=/mingw64
mkdir -p "$out"

echo "collecting dependent DLLs"
# ldd walks the whole tree, so this catches what the direct dependencies pull
# in as well. Only the MSYS2 ones are copied; the Windows system DLLs are
# already on the target machine and shipping them would be wrong.
ldd "$out/astral.exe" \
  | awk '{print $3}' \
  | grep -i "^/mingw64/" \
  | sort -u \
  | while read -r dll; do cp -u "$dll" "$out/"; done

echo "collecting GSettings schemas"
mkdir -p "$out/share/glib-2.0/schemas"
cp "$mingw/share/glib-2.0/schemas/gschemas.compiled" "$out/share/glib-2.0/schemas/"

echo "collecting pixbuf loaders"
loaders=$(find "$mingw/lib/gdk-pixbuf-2.0" -maxdepth 1 -type d -name '2.*' | head -1)
if [ -n "$loaders" ]; then
  mkdir -p "$out/lib/gdk-pixbuf-2.0/$(basename "$loaders")"
  cp -r "$loaders/." "$out/lib/gdk-pixbuf-2.0/$(basename "$loaders")/"
fi

echo "collecting the icon theme"
mkdir -p "$out/share/icons"
cp -r "$mingw/share/icons/Adwaita" "$out/share/icons/" 2>/dev/null || \
  echo "  (no Adwaita icon theme found; stock icons may be missing)"
if [ -d "$mingw/share/icons/hicolor" ]; then
  cp -r "$mingw/share/icons/hicolor" "$out/share/icons/"
fi

echo "collecting GTK's own resources"
mkdir -p "$out/share/glib-2.0"
[ -d "$mingw/share/gtk-4.0" ] && cp -r "$mingw/share/gtk-4.0" "$out/share/" || true

echo
echo "bundle contents:"
du -sh "$out"
find "$out" -maxdepth 1 -name '*.dll' | wc -l | xargs echo "  DLLs:"
