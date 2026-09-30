#!/usr/bin/env bash
# Installs, updates and uninstalls Astral on Linux.
#
#   curl -fsSL https://raw.githubusercontent.com/EternalCoder454/astral/release/install.sh | bash
#   curl -fsSL https://raw.githubusercontent.com/EternalCoder454/astral/release/install.sh | bash -s -- uninstall
#
# Two routes. Where the distribution ships libadwaita 1.9 or newer, Astral is
# built from source, the same way `make install` does it. Everywhere else it is
# installed from the Flatpak attached to the latest GitHub release.
#
# Everything is inside main, called on the last line, so a download that is cut
# short runs nothing at all.

set -euo pipefail

REPO_URL="${ASTRAL_REPO_URL:-https://github.com/EternalCoder454/astral.git}"
API_URL="${ASTRAL_API_URL:-https://api.github.com/repos/EternalCoder454/astral/releases/latest}"
DL_BASE="${ASTRAL_DL_BASE:-https://github.com/EternalCoder454/astral/releases/download}"
FLATPAK_URL="${ASTRAL_FLATPAK_URL:-}"
APP_ID="io.github.astral"
MIN_ADW="1.9"

# Native install paths: the same three files the Makefile writes.
PREFIX="$HOME/.local"
BIN_FILE="$PREFIX/bin/astral"
DESKTOP_FILE="$PREFIX/share/applications/$APP_ID.desktop"
ICON_FILE="$PREFIX/share/icons/hicolor/scalable/apps/$APP_ID.svg"

# Astral's own data, like the app's updater: under XDG_DATA_HOME.
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
ASTRAL_DATA="$DATA_HOME/astral"
SRC_DIR="$ASTRAL_DATA/src"
GO_DIR="$ASTRAL_DATA/go"
ASTRAL_CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/astral"
ASTRAL_CACHE="${XDG_CACHE_HOME:-$HOME/.cache}/astral"
FLATPAK_DATA="$HOME/.var/app/$APP_ID"

CMD="install"
ROUTE=""          # native or flatpak, empty to decide
BRANCH="release"
DELETE_LIBRARY=0
ASSUME_YES=0
BUILD_DIR=""

PM=""             # dnf apt pacman zypper xbps, empty if none
DISTRO_NAME=""
TMP_FILE=""

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_B=$'\033[1m'; C_G=$'\033[32m'; C_Y=$'\033[33m'; C_R=$'\033[31m'; C_0=$'\033[0m'
else
  C_B=""; C_G=""; C_Y=""; C_R=""; C_0=""
fi

say()  { printf '%s\n' "$*"; }
step() { printf '%s==>%s %s\n' "$C_B" "$C_0" "$*"; }
ok()   { printf '%s ok%s %s\n' "$C_G" "$C_0" "$*"; }
warn() { printf '%swarn%s %s\n' "$C_Y" "$C_0" "$*" >&2; }
die()  { printf '%serror%s %s\n' "$C_R" "$C_0" "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Astral installer

Usage: install.sh [command] [options]

Commands:
  install      Install Astral, or update it if it is already installed (default)
  update       Update an existing install
  uninstall    Remove Astral, keeping your library unless told otherwise
  build        Build and install from source (used by the app's updater)

Options:
  --native           Build from source, even where the Flatpak would be chosen
  --flatpak          Use the Flatpak, even where a native build is possible
  --beta             Native only: follow the beta branch instead of release
  --delete-library   With uninstall: also delete your library (asks you to type "delete")
  --yes              Do not ask questions
  --help             Show this text

Environment: ASTRAL_FLATPAK_URL overrides where the Flatpak is downloaded from.
EOF
}

cleanup() {
  if [ -n "$TMP_FILE" ]; then rm -f "$TMP_FILE"; fi
}

# ---------------------------------------------------------------- helpers

have() { command -v "$1" >/dev/null 2>&1; }

fetch() { # fetch URL [OUTFILE]; prints to stdout without OUTFILE
  local url="$1" out="${2:-}"
  if have curl; then
    if [ -n "$out" ]; then curl -fsSL "$url" -o "$out"; else curl -fsSL "$url"; fi
  elif have wget; then
    if [ -n "$out" ]; then wget -qO "$out" "$url"; else wget -qO- "$url"; fi
  else
    die "Neither curl nor wget is installed. Install one of them and run this again."
  fi
}

# Normalises 1.26 to 1.26.0 so sort -V compares like with like.
norm() {
  local v="${1%%[-+ ]*}"
  case "$v" in
    *.*.*) printf '%s' "$v" ;;
    *.*)   printf '%s.0' "$v" ;;
    *)     printf '%s.0.0' "$v" ;;
  esac
}

ver_ge() { # ver_ge A B: A >= B
  local a b
  a="$(norm "$1")"; b="$(norm "$2")"
  [ "$(printf '%s\n%s\n' "$b" "$a" | sort -V | head -n1)" = "$b" ]
}

# Asks y/N on the terminal, since stdin is the piped script.
confirm() {
  [ "$ASSUME_YES" = 1 ] && return 0
  local reply=""
  if ! { exec 3</dev/tty; } 2>/dev/null; then
    die "No terminal to ask on. Run this again with --yes to go ahead without questions."
  fi
  printf '%s [y/N] ' "$1" >&2
  read -r reply <&3 || reply=""
  exec 3<&-
  case "$reply" in y|Y|yes|YES|Yes) return 0 ;; *) return 1 ;; esac
}

SUDO=()
need_root() {
  if [ "$(id -u)" = 0 ]; then
    SUDO=()
  elif have sudo; then
    SUDO=(sudo)
  else
    die "This step needs root and sudo is not installed. Run the command above as root, then run this again."
  fi
}

# as_root DESCRIPTION CMD...: shows the command, asks, runs it as root.
as_root() {
  local why="$1"; shift
  need_root
  say ""
  say "$why"
  say "  ${SUDO[*]:+${SUDO[*]} }$*"
  confirm "Run this?" || die "Stopped. Nothing was installed."
  "${SUDO[@]}" "$@" || die "That command failed, see its output above."
}

astral_running() { have pgrep && pgrep -x astral >/dev/null 2>&1; }

# library_path_ok says a directory is safe to delete as part of the library:
# absolute, named astral, and neither the root nor the home directory. The
# paths come from XDG variables, which can hold anything.
library_path_ok() {
  case "$1" in
    /*) ;;
    *) return 1 ;;
  esac
  [ "$1" != / ] && [ "$1" != "$HOME" ] && [ "$(basename "$1")" = astral ]
}

refresh_desktop() {
  update-desktop-database "$PREFIX/share/applications" >/dev/null 2>&1 || true
  gtk-update-icon-cache -f -t "$PREFIX/share/icons/hicolor" >/dev/null 2>&1 || true
}

# ---------------------------------------------------------------- distro

detect_distro() {
  local id="" like="" name="" w
  # shellcheck disable=SC1091
  if [ -r /etc/os-release ]; then
    id="$(. /etc/os-release; printf '%s' "${ID:-}")"
    like="$(. /etc/os-release; printf '%s' "${ID_LIKE:-}")"
    name="$(. /etc/os-release; printf '%s' "${PRETTY_NAME:-${NAME:-}}")"
  fi
  DISTRO_NAME="${name:-${id:-unknown}}"
  PM=""

  # An image based system cannot take build packages at all.
  if [ -e /run/ostree-booted ]; then return 0; fi

  for w in $id $like; do
    case "$w" in
      fedora|rhel|centos|nobara|rocky|almalinux) PM=dnf ;;
      debian|ubuntu|linuxmint|pop|zorin|elementary) PM=apt ;;
      arch|manjaro|endeavouros|cachyos|garuda) PM=pacman ;;
      opensuse*|suse|sles) PM=zypper ;;
      void) PM=xbps ;;
    esac
    [ -n "$PM" ] && break
  done

  if [ -n "$PM" ]; then
    local bin="$PM"
    [ "$PM" = apt ] && bin=apt-get
    [ "$PM" = xbps ] && bin=xbps-install
    have "$bin" || PM=""
  fi
  return 0
}

# Prints the libadwaita version the repositories offer, or nothing.
repo_adw_version() {
  local v=""
  case "$PM" in
    apt)
      v="$(apt-cache policy libadwaita-1-dev 2>/dev/null | sed -n 's/^ *Candidate: *//p')"
      [ "$v" = "(none)" ] && v=""
      v="$(printf '%s' "$v" | sed 's/^[0-9]*://; s/[-+~].*//')"
      ;;
    dnf)
      v="$(dnf -q repoquery --latest-limit=1 --qf '%{version}\n' libadwaita-devel 2>/dev/null | head -n1)"
      ;;
    pacman)
      v="$(pacman -Si libadwaita 2>/dev/null | sed -n 's/^Version *: *//p' | sed 's/^[0-9]*://; s/-[0-9.]*$//')"
      ;;
    zypper)
      v="$(zypper -n --gpg-auto-import-keys info -t package libadwaita-devel 2>/dev/null | sed -n 's/^Version *: *//p' | head -n1 | sed 's/-.*//')"
      ;;
    xbps)
      v="$(xbps-query -R -p pkgver libadwaita-devel 2>/dev/null | sed 's/^.*-//; s/_.*//')"
      ;;
  esac
  printf '%s' "$v"
}

# Sets ROUTE_REASON and returns 0 if the distribution can build natively.
native_possible() {
  ROUTE_REASON=""
  if [ -e /run/ostree-booted ]; then
    ROUTE_REASON="this is an image based system, which cannot take build packages"
    return 1
  fi
  if [ -z "$PM" ]; then
    ROUTE_REASON="no supported package manager found for $DISTRO_NAME"
    return 1
  fi
  local v
  v="$(repo_adw_version)"
  if [ -z "$v" ] && [ "$PM" = apt ]; then
    # A fresh machine has no package lists to ask.
    as_root "The package lists are empty, so I cannot see which libadwaita $DISTRO_NAME offers. Refresh them with:" apt-get update
    v="$(repo_adw_version)"
  elif [ -z "$v" ] && [ "$PM" = zypper ]; then
    as_root "The repository metadata is not loaded, so I cannot see which libadwaita $DISTRO_NAME offers. Refresh it with:" zypper -n --gpg-auto-import-keys refresh
    v="$(repo_adw_version)"
  fi
  if [ -z "$v" ]; then
    if [ "$PM" = pacman ]; then
      # Arch is rolling and has been past 1.9 for a long time; the check after
      # the install still confirms it.
      return 0
    fi
    ROUTE_REASON="could not find libadwaita in the $PM repositories"
    return 1
  fi
  if ver_ge "$v" "$MIN_ADW"; then
    return 0
  fi
  ROUTE_REASON="$DISTRO_NAME ships libadwaita $v and Astral needs $MIN_ADW or newer"
  return 1
}

build_packages() {
  case "$PM" in
    dnf)
      echo gcc git make tar gzip pkgconf-pkg-config gtk4-devel libadwaita-devel gobject-introspection-devel ;;
    apt)
      local gi=libgirepository1.0-dev
      # Newer releases may drop the 1.0 headers; 2.0 is what remains.
      if ! apt-cache show libgirepository1.0-dev >/dev/null 2>&1 \
         && apt-cache show libgirepository-2.0-dev >/dev/null 2>&1; then
        gi=libgirepository-2.0-dev
      fi
      echo build-essential git make tar pkg-config libgtk-4-dev libadwaita-1-dev gobject-introspection "$gi" ;;
    pacman)
      echo gcc git make tar pkgconf gtk4 libadwaita gobject-introspection ;;
    zypper)
      echo gcc git make tar gzip pkgconf-pkg-config gtk4-devel libadwaita-devel gobject-introspection-devel ;;
    xbps)
      echo gcc git make tar pkg-config gtk4-devel libadwaita-devel gobject-introspection ;;
  esac
}

install_cmd() { # install_cmd PKGS...
  case "$PM" in
    dnf)    echo dnf -y install "$@" ;;
    apt)    echo apt-get install -y "$@" ;;
    pacman)
      if ls /var/lib/pacman/sync/*.db >/dev/null 2>&1; then
        echo pacman -S --needed --noconfirm "$@"
      else
        echo pacman -Sy --needed --noconfirm "$@"
      fi ;;
    zypper) echo zypper -n install "$@" ;;
    xbps)   echo xbps-install -Sy "$@" ;;
  esac
}

install_build_packages() {
  local pkgs cmd
  pkgs="$(build_packages)"
  # shellcheck disable=SC2086
  cmd="$(install_cmd $pkgs)"
  step "Installing build packages with $PM"
  # shellcheck disable=SC2086
  as_root "These packages are needed to build Astral:" $cmd
}

# ---------------------------------------------------------------- Go

go_version_of() { # go_version_of GOBIN
  GOTOOLCHAIN=local "$1" version 2>/dev/null | sed -n 's/^go version go\([0-9.]*\).*/\1/p'
}

go_required() { # go_required SRCDIR
  sed -n 's/^go  *\([0-9.]*\).*/\1/p' "$1/go.mod" | head -n1
}

ensure_go() { # ensure_go SRCDIR; leaves a good go first on PATH
  local need have_v g arch goarch json line fname sha tmp sum
  need="$(go_required "$1")"
  [ -n "$need" ] || die "Could not read the Go version from $1/go.mod."

  if have go; then
    have_v="$(go_version_of "$(command -v go)")"
    if [ -n "$have_v" ] && ver_ge "$have_v" "$need"; then
      ok "Using Go $have_v from PATH"
      export GOTOOLCHAIN=local
      return 0
    fi
  fi
  g="$GO_DIR/bin/go"
  if [ -x "$g" ]; then
    have_v="$(go_version_of "$g")"
    if [ -n "$have_v" ] && ver_ge "$have_v" "$need"; then
      ok "Using Go $have_v from $GO_DIR"
      PATH="$GO_DIR/bin:$PATH"; export PATH GOTOOLCHAIN=local
      return 0
    fi
  fi

  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64)  goarch=amd64 ;;
    aarch64|arm64) goarch=arm64 ;;
    *) die "No Go toolchain for $arch. Install Go $need or newer yourself and run this again." ;;
  esac

  step "Downloading Go for $goarch (Astral needs $need or newer)"
  json="$(fetch 'https://go.dev/dl/?mode=json&include=all')" || die "Could not reach go.dev. Check your connection and run this again."
  # The newest stable release comes first. Its filename line is followed by its
  # sha256 line inside the same object; release candidates do not match.
  local want=0 fn="" l re
  re="^go[0-9]+\.[0-9]+(\.[0-9]+)?\.linux-$goarch\.tar\.gz$"
  line=""
  while IFS= read -r l; do
    case "$l" in
      *'"filename"'*)
        fn="${l#*\"filename\": \"}"; fn="${fn%%\"*}"
        want=0
        if [[ "$fn" =~ $re ]]; then want=1; fi ;;
      *'"sha256"'*)
        if [ "$want" = 1 ]; then
          l="${l#*\"sha256\": \"}"
          line="$fn ${l%%\"*}"
          break
        fi ;;
    esac
  done < <(printf '%s\n' "$json" | grep -E '"(filename|sha256)"')
  [ -n "$line" ] || die "go.dev lists no Go download for linux-$goarch."
  fname="${line% *}"; sha="${line#* }"
  have_v="${fname#go}"; have_v="${have_v%.linux-*}"
  ver_ge "$have_v" "$need" || die "The newest Go on go.dev is $have_v, older than the $need Astral needs."

  tmp="$(mktemp -d)"
  fetch "https://go.dev/dl/$fname" "$tmp/$fname" || { rm -rf "$tmp"; die "Go download failed. Run this again."; }
  sum="$(sha256sum "$tmp/$fname")"
  if [ "${sum%% *}" != "$sha" ]; then
    rm -rf "$tmp"
    die "The Go download did not match its published checksum, so it was discarded. Run this again."
  fi
  mkdir -p "$ASTRAL_DATA"
  rm -rf "$GO_DIR"
  tar -C "$ASTRAL_DATA" -xzf "$tmp/$fname"
  rm -rf "$tmp"
  ok "Installed Go $have_v in $GO_DIR"
  PATH="$GO_DIR/bin:$PATH"; export PATH GOTOOLCHAIN=local
}

# ---------------------------------------------------------------- native

native_installed() {
  [ -e "$BIN_FILE" ] || [ -e "$DESKTOP_FILE" ]
}

sync_clone() {
  step "Fetching Astral ($BRANCH branch)"
  # Mirrors updateScript in internal/app/update_unix.go.
  # Every step ends in die: do_native runs under ||, where set -e does not
  # reach, and a failed fetch must not go on to report an install.
  if [ ! -d "$SRC_DIR/.git" ]; then
    rm -rf "$SRC_DIR"
    mkdir -p "$(dirname "$SRC_DIR")"
    git clone --quiet --depth 1 --branch "$BRANCH" "$REPO_URL" "$SRC_DIR" \
      || die "Could not download Astral's source. Check your connection and run this again."
  fi
  git -C "$SRC_DIR" fetch --quiet --depth 1 --prune origin "$BRANCH" \
    || die "Could not fetch the $BRANCH branch. Check your connection and run this again."
  git -C "$SRC_DIR" checkout --quiet -B "$BRANCH" FETCH_HEAD || die "Could not check out the $BRANCH branch."
  git -C "$SRC_DIR" reset --quiet --hard FETCH_HEAD || die "Could not check out the $BRANCH branch."
}

make_install() { # make_install DIR
  step "Building Astral (the first build takes several minutes)"
  make -C "$1" install || die "The build failed, see the output above. Install the build packages (gtk4, libadwaita $MIN_ADW or newer, gobject-introspection headers, gcc) and try again."
}

prune_clone() {
  rm -f "$SRC_DIR/bin/astral"
  git -C "$SRC_DIR" for-each-ref --format='%(refname)' refs/remotes refs/tags | while read -r ref; do
    git -C "$SRC_DIR" update-ref -d "$ref"
  done
  git -C "$SRC_DIR" reflog expire --expire=now --all || true
  git -C "$SRC_DIR" gc --prune=now --quiet || true
}

native_report() {
  say ""
  ok "Astral is installed"
  say "  Program:  $BIN_FILE"
  say "  Launcher: $DESKTOP_FILE"
  say "  Launch it by searching for Astral in your application menu"
  case ":$PATH:" in
    *":$PREFIX/bin:"*) say "  or by running: astral" ;;
    *) say "  or by running: $BIN_FILE (add $PREFIX/bin to PATH to use just: astral)" ;;
  esac
}

do_native() {
  if ! pkg-config --atleast-version="$MIN_ADW" libadwaita-1 >/dev/null 2>&1 \
     || ! pkg-config --exists gobject-introspection-1.0 >/dev/null 2>&1 \
     || ! have gcc || ! have make || ! have git; then
    [ -n "$PM" ] || die "Cannot install build packages here: no supported package manager. Use --flatpak instead."
    install_build_packages
  else
    ok "Build packages are already installed"
  fi
  if ! pkg-config --atleast-version="$MIN_ADW" libadwaita-1 >/dev/null 2>&1; then
    return 2
  fi
  sync_clone
  ensure_go "$SRC_DIR"
  make_install "$SRC_DIR"
  prune_clone
  native_report
}

do_build() {
  local dir="${BUILD_DIR:-$SRC_DIR}"
  [ -f "$dir/go.mod" ] || die "No Astral source in $dir."
  have make || die "make is not installed."
  ensure_go "$dir"
  make_install "$dir"
}

# ---------------------------------------------------------------- flatpak

flatpak_scope() { # prints user and/or system if installed
  have flatpak || return 0
  flatpak info --user "$APP_ID" >/dev/null 2>&1 && echo user
  flatpak info --system "$APP_ID" >/dev/null 2>&1 && echo system
  return 0
}

do_flatpak() {
  local arch scopes url ver tag
  arch="$(uname -m)"
  if [ "$arch" != x86_64 ]; then
    die "The Flatpak is built for x86_64 only and this machine is $arch. Astral can be built from source only where libadwaita $MIN_ADW is available."
  fi

  if ! have flatpak; then
    [ -n "$PM" ] || die "Flatpak is not installed and I do not know how to install it on $DISTRO_NAME. Install flatpak (https://flatpak.org/setup/), then run this again."
    if [ "$PM" = apt ] && ! apt-cache policy flatpak 2>/dev/null | grep -q 'Candidate: [0-9]'; then
      as_root "The package lists do not know flatpak yet. Refresh them with:" apt-get update
    fi
    # shellcheck disable=SC2046
    as_root "Astral's Flatpak needs flatpak itself:" $(install_cmd flatpak)
    have flatpak || die "flatpak did not install. Install it yourself, then run this again."
  fi

  scopes="$(flatpak_scope)"
  # A system install is updated where it is; otherwise, and when both exist,
  # the user's own.
  local scope=user
  [ "$scopes" = system ] && scope=system

  step "Adding the Flathub remote (for the runtime)"
  if [ "$scope" = system ]; then
    need_root
    "${SUDO[@]}" flatpak remote-add --system --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo
  else
    flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo
  fi

  url="$FLATPAK_URL"
  if [ -z "$url" ]; then
    step "Looking up the latest release"
    tag="$(fetch "$API_URL" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)" || true
    [ -n "$tag" ] || die "Could not read the latest release from GitHub. Check your connection, or set ASTRAL_FLATPAK_URL."
    ver="${tag#v}"
    url="$DL_BASE/v$ver/astral-$ver-x86_64.flatpak"
  fi

  TMP_FILE="$(mktemp --suffix=.flatpak)"
  step "Downloading $url"
  fetch "$url" "$TMP_FILE" || die "Download failed. If no Flatpak is published for this release yet, try --native on a distribution with libadwaita $MIN_ADW."

  step "Installing the Flatpak ($scope)"
  if [ "$scope" = system ]; then
    need_root
    "${SUDO[@]}" flatpak install --system --noninteractive -y --bundle "$TMP_FILE"
  else
    flatpak install --user --noninteractive -y --bundle "$TMP_FILE"
  fi
  rm -f "$TMP_FILE"; TMP_FILE=""

  say ""
  ok "Astral is installed as a Flatpak"
  say "  Launch it from your application menu, or run: flatpak run $APP_ID"
  say "  Your library lives in $FLATPAK_DATA"
  say "  GNOME Software, Discover or the Software Manager can also remove it."
  if astral_running; then
    say ""
    say "  NOTE: Astral is already running, and will keep using the previous build."
    say "        Quit it and open it again to pick this one up."
  fi
}

# ---------------------------------------------------------------- commands

do_install() {
  local native=0 scopes
  native_installed && native=1
  scopes="$(flatpak_scope | tr '\n' ' ')"

  if [ -z "$ROUTE" ]; then
    if [ "$native" = 1 ]; then
      ROUTE=native; say "Astral is installed natively, updating it."
    elif [ -n "$scopes" ]; then
      ROUTE=flatpak; say "Astral is installed as a Flatpak, updating it."
    fi
  fi

  if [ -z "$ROUTE" ]; then
    if native_possible; then
      ROUTE=native
      ok "$DISTRO_NAME offers libadwaita $MIN_ADW or newer: building from source"
    else
      ROUTE=flatpak
      ok "Using the Flatpak: $ROUTE_REASON"
    fi
  fi

  if [ "$ROUTE" = native ]; then
    if [ "$native" = 0 ] && [ -n "$scopes" ] && [ "$CMD" = install ]; then
      warn "Astral is also installed as a Flatpak. Both will exist side by side."
    fi
    local rc=0
    do_native || rc=$?
    if [ "$rc" = 2 ]; then
      if [ -z "${FORCED_NATIVE:-}" ]; then
        warn "libadwaita $MIN_ADW is still not available after installing packages. Falling back to the Flatpak."
        ROUTE=flatpak
        do_flatpak
      else
        die "libadwaita $MIN_ADW or newer is not available here, so Astral cannot be built. Use --flatpak instead."
      fi
    elif [ "$rc" != 0 ]; then
      exit "$rc"
    fi
  else
    do_flatpak
  fi
}

# confirm_delete_library asks, before anything is removed, whether the library
# really goes too. Asked first because the Flatpak's data goes in the same step
# as the Flatpak itself.
confirm_delete_library() {
  [ "$DELETE_LIBRARY" = 1 ] || return 0
  [ -n "$HOME" ] || die "Refusing to delete with HOME unset."
  say "This deletes your library, everything Astral saved:"
  say "  $ASTRAL_DATA"
  say "  $ASTRAL_CONFIG"
  say "  $ASTRAL_CACHE"
  [ -d "$FLATPAK_DATA" ] && say "  $FLATPAK_DATA"
  [ "$ASSUME_YES" = 1 ] && return 0
  local reply=""
  if ! { exec 3</dev/tty; } 2>/dev/null; then
    die "No terminal to ask on. Run this again with --yes to go ahead without questions."
  fi
  printf 'Type delete to confirm: ' >&2
  read -r reply <&3 || reply=""
  exec 3<&-
  if [ "$reply" != delete ]; then
    say "Not confirmed, so your library will be kept."
    DELETE_LIBRARY=0
  fi
  say ""
}

do_uninstall() {
  local found=0 scopes s
  confirm_delete_library
  step "Removing Astral"

  # The Flatpak first: it is the step that can fail or ask for a password,
  # and stopping there leaves everything in place rather than half removed.
  scopes="$(flatpak_scope | tr '\n' ' ')"
  for s in $scopes; do
    found=1
    local args=(-y)
    [ "$DELETE_LIBRARY" = 1 ] && args+=(--delete-data)
    if [ "$s" = system ]; then
      as_root "Removing the system Flatpak needs root:" flatpak uninstall --system "${args[@]}" "$APP_ID"
    else
      flatpak uninstall --user "${args[@]}" "$APP_ID" || die "Could not remove the Flatpak, see the output above. Nothing else was removed."
    fi
    ok "Removed the $s Flatpak"
  done

  if native_installed || [ -d "$SRC_DIR" ] || [ -d "$GO_DIR" ]; then
    found=1
    rm -f "$BIN_FILE" "$DESKTOP_FILE" "$ICON_FILE"
    rm -rf "$SRC_DIR" "$GO_DIR"
    refresh_desktop
    ok "Removed the native install, its source clone and its private Go"
  fi

  [ "$found" = 1 ] || say "No Astral program was installed, nothing to remove but the library."

  if [ "$DELETE_LIBRARY" = 1 ]; then
    local d
    for d in "$ASTRAL_DATA" "$ASTRAL_CONFIG" "$ASTRAL_CACHE"; do
      library_path_ok "$d" || die "Refusing to delete $d, which does not look like Astral's own folder."
    done
    rm -rf "$ASTRAL_DATA" "$ASTRAL_CONFIG" "$ASTRAL_CACHE"
    if [ -d "$FLATPAK_DATA" ]; then rm -rf "$FLATPAK_DATA"; fi
    ok "Deleted your library"
  else
    say ""
    say "Kept your library (delete it with: uninstall --delete-library):"
    [ -d "$ASTRAL_DATA" ] && say "  $ASTRAL_DATA"
    [ -d "$ASTRAL_CONFIG" ] && say "  $ASTRAL_CONFIG"
    [ -d "$ASTRAL_CACHE" ] && say "  $ASTRAL_CACHE"
    [ -d "$FLATPAK_DATA" ] && say "  $FLATPAK_DATA"
  fi
  return 0
}

main() {
  trap cleanup EXIT
  local arg
  local args=("$@")
  local n=0
  while [ "$n" -lt "${#args[@]}" ]; do
    arg="${args[$n]}"
    case "$arg" in
      install|update|uninstall|build) CMD="$arg"
        if [ "$arg" = build ] && [ $((n + 1)) -lt "${#args[@]}" ]; then
          case "${args[$((n + 1))]}" in -*) ;; *) BUILD_DIR="${args[$((n + 1))]}"; n=$((n + 1)) ;; esac
        fi ;;
      --native)  ROUTE=native; FORCED_NATIVE=1 ;;
      --flatpak) ROUTE=flatpak ;;
      --beta)    BRANCH=beta ;;
      --delete-library) DELETE_LIBRARY=1 ;;
      --yes|-y)  ASSUME_YES=1 ;;
      --help|-h) usage; return 0 ;;
      *) usage >&2; die "Unknown argument: $arg" ;;
    esac
    n=$((n + 1))
  done

  [ "$(uname -s)" = Linux ] || die "This installer is for Linux."
  [ "$(id -u)" != 0 ] || [ "$CMD" = build ] || [ "${ASTRAL_ALLOW_ROOT:-}" = 1 ] || [ -e /.dockerenv ] || [ -e /run/.containerenv ] \
    || die "Run this as your normal user, not root. It asks for sudo only when it needs it."
  [ -n "${HOME:-}" ] || die "HOME is not set."

  if [ "$ROUTE" = flatpak ] && [ "$BRANCH" = beta ]; then
    warn "--beta only applies to native installs and is ignored for the Flatpak."
  fi

  detect_distro
  case "$CMD" in
    build)     do_build ;;
    uninstall) do_uninstall ;;
    install|update) do_install ;;
  esac
}

main "$@"
