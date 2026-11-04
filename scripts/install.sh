#!/usr/bin/env bash
# Installs Lookback for the current user, without root: the binary, the desktop entry and the icon under
# PREFIX (default ~/.local). Running it again replaces the installed files (idempotent). --uninstall removes
# them. It never edits compositor or shell configuration; it prints the bind line for the user to add.
#
#   scripts/install.sh [--prefix DIR] [--binary PATH]
#   scripts/install.sh --uninstall [--prefix DIR]
#
# Without --binary, the script builds ./cmd/lookback from this repository with the Go toolchain.
# To install the .deb or tarball output of `mygo build` instead, pass its binary with --binary.
set -euo pipefail

here=$(cd "$(dirname "$0")/.." && pwd)
prefix="${HOME}/.local"
binary=""
mode=install

usage() {
  sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix) [ $# -ge 2 ] || { echo "install.sh: --prefix needs a directory" >&2; exit 2; }; prefix="$2"; shift 2 ;;
    --prefix=*) prefix="${1#--prefix=}"; shift ;;
    --binary) [ $# -ge 2 ] || { echo "install.sh: --binary needs a file" >&2; exit 2; }; binary="$2"; shift 2 ;;
    --binary=*) binary="${1#--binary=}"; shift ;;
    --uninstall) mode=uninstall; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "$prefix" in /*) ;; *) echo "install.sh: --prefix must be an absolute path" >&2; exit 2 ;; esac

bin_dir="$prefix/bin"
app_dir="$prefix/share/applications"
# hicolor has no 1024x1024 size, so launchers skip that folder: install the SVG (scalable) and a 512 px PNG.
icon_root="$prefix/share/icons/hicolor"
icon_dirs="$icon_root/scalable/apps $icon_root/512x512/apps"
bin_path="$bin_dir/lookback"
desktop_path="$app_dir/lookback.desktop"
icon_files="$icon_root/scalable/apps/lookback.svg $icon_root/512x512/apps/lookback.png $icon_root/1024x1024/apps/lookback.png"

refresh_caches() {
  # Optional icon cache: a missing tool or a failed refresh does not stop the install.
  if command -v gtk-update-icon-cache >/dev/null 2>&1 && [ -f "$prefix/share/icons/hicolor/index.theme" ]; then
    gtk-update-icon-cache -q -t "$prefix/share/icons/hicolor" >/dev/null 2>&1 || true
  fi
  touch "$prefix/share/icons/hicolor" 2>/dev/null || true # makes GTK drop a stale icon-theme.cache
}

if [ "$mode" = uninstall ]; then
  rm -f "$bin_path" "$desktop_path" $icon_files
  refresh_caches
  echo "Removed Lookback from $prefix (settings and data are kept)."
  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

if [ -z "$binary" ]; then
  command -v go >/dev/null 2>&1 || { echo "install.sh: go is needed to build Lookback; pass --binary to install a built one" >&2; exit 1; }
  echo "Building Lookback from $here ..."
  (cd "$here" && CGO_ENABLED=0 go build -trimpath -o "$work/lookback" ./cmd/lookback)
  binary="$work/lookback"
fi
[ -f "$binary" ] || { echo "install.sh: no binary at $binary" >&2; exit 1; }

mkdir -p "$bin_dir" "$app_dir" $icon_dirs
install -m 0755 "$binary" "$bin_path"
install -m 0644 "$here/resources/icon.svg" "$icon_root/scalable/apps/lookback.svg"
install -m 0644 "$here/resources/icon-512.png" "$icon_root/512x512/apps/lookback.png"
rm -f "$icon_root/1024x1024/apps/lookback.png" # left by earlier versions
# The desktop file name (lookback.desktop) is the app ID that the Wayland GlobalShortcuts portal reads.
cat > "$work/lookback.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Lookback
Comment=Semantic search over your local files
Exec=$bin_path
Icon=lookback
Categories=Utility;FileTools;
Terminal=false
DESKTOP
install -m 0644 "$work/lookback.desktop" "$desktop_path"
refresh_caches

echo "Installed Lookback to $prefix:"
echo "  $bin_path"
echo "  $desktop_path"
echo "  $icon_root/scalable/apps/lookback.svg"
case ":$PATH:" in
  *":$bin_dir:"*) ;;
  *) echo "Note: $bin_dir is not on PATH, so 'lookback --toggle' needs the full path." ;;
esac
echo
echo "On Hyprland, add this line to your Hyprland Lua config, for example ~/.config/hypr/bindings.lua"
echo "(the script does not edit it):"
echo '  hl.bind("SUPER + period", hl.dsp.exec_cmd("lookback --toggle"))'
