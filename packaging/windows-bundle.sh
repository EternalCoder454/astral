#!/usr/bin/env bash
# Collect everything astral.exe needs to run on a machine without MSYS2.
#
# A GTK application is not one file. It needs its own DLLs and every DLL those
# pull in, the GSettings schemas GTK reads at startup, the pixbuf loaders that
# decode images, and an icon theme for the stock icons the window controls use.
# Miss any of them and it either refuses to start or starts and renders
# nothing, which is a bad way to find out.
set -euo pipefail

out="${1:-dist}"
mingw=/mingw64
mkdir -p "$out"

# The runtime data comes first, because the DLL closure below is computed over
# everything in the bundle and the pixbuf loaders are part of what needs
# resolving.

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
[ -d "$mingw/share/gtk-4.0" ] && cp -r "$mingw/share/gtk-4.0" "$out/share/" || true

# cairo links fontconfig on every platform, so fontconfig is in the bundle
# whether or not Pango uses it. Without a configuration file it complains on
# startup, and the file costs nothing.
if [ -f "$mingw/etc/fonts/fonts.conf" ]; then
  echo "collecting the fontconfig configuration"
  mkdir -p "$out/etc/fonts"
  cp -r "$mingw/etc/fonts/." "$out/etc/fonts/"
fi

# Now the DLLs, as a closure over the whole bundle rather than over the
# executable.
#
# Walking astral.exe alone is not enough, and the shortfall is invisible until
# someone runs it. The pixbuf loaders are opened at runtime, so nothing links
# them and their own dependencies never appear in the executable's tree: the
# SVG loader needs librsvg, and the icon theme is 729 SVGs against 51 PNGs, so
# a bundle without it starts and draws no icons. Each pass can pull in a
# library that has dependencies of its own, so it repeats until the set stops
# growing.
echo "collecting dependent DLLs"
pass=0
while :; do
  before=$(find "$out" -name '*.dll' | wc -l)
  find "$out" \( -name '*.dll' -o -name '*.exe' \) -print0 \
    | xargs -0 -r -n1 ldd 2>/dev/null \
    | awk '{print $3}' \
    | grep -i "^/mingw64/" \
    | sort -u \
    | while read -r dll; do cp -u "$dll" "$out/"; done
  after=$(find "$out" -name '*.dll' | wc -l)
  pass=$((pass + 1))
  echo "  pass $pass: $before -> $after"
  [ "$after" -eq "$before" ] && break
done

# systemDLLs is what Windows itself provides. Everything else an import names
# has to be in the bundle, and this is the check that says so before a user
# finds out. It is the check librsvg went missing under.
echo
echo "checking that every import resolves"
system_dlls=$(cat <<'EOF'
advapi32 api-ms-win authz avicap32 avrt bcrypt bcryptprimitives cabinet cfgmgr32
combase comctl32 comdlg32 crypt32 d2d1 d3d9 d3d10 d3d11 d3d12 dbghelp dcomp
dinput8 dnsapi dsound dwmapi dwrite dxgi dxva2 gdi32 gdi32full gdiplus glu32
hid imm32 iphlpapi kernel32 kernelbase ksuser mf mfplat mfreadwrite mpr msacm32
msimg32 msvcp msvcrt ncrypt netapi32 normaliz ntdll ole32 oleacc oleaut32
opengl32 pdh powrprof propsys psapi rpcrt4 rstrtmgr secur32 sechost setupapi
shcore shell32 shlwapi sspicli synchronization ucrtbase user32 userenv usp10
uxtheme version vulkan-1 win32u windowscodecs winmm winspool wintrust wtsapi32
ws2_32 xinput1
EOF
)
report=$(mktemp)
while read -r pe; do
  objdump -p "$pe" \
    | sed -n 's/^\tDLL Name: //p' \
    | tr 'A-Z' 'a-z' \
    | while read -r dep; do
        [ -f "$out/$dep" ] && continue
        base=${dep%.dll}; base=${base%.drv}
        for sys in $system_dlls; do
          case "$base" in "$sys"*) continue 2;; esac
        done
        echo "  MISSING $dep, needed by ${pe#"$out"/}"
      done
done < <(find "$out" \( -name '*.dll' -o -name '*.exe' \) | sort) | tee "$report"
if [ -s "$report" ]; then
  echo
  echo "the bundle is incomplete; add the libraries above" >&2
  exit 1
fi
echo "  every import is either bundled or provided by Windows"

echo
echo "bundle contents:"
du -sh "$out"
find "$out" -maxdepth 1 -name '*.dll' | wc -l | xargs echo "  DLLs:"
