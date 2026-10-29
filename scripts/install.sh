#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
TASK_INSTALL_OPEN=true
if [ "$#" -gt 1 ]; then echo '用法：install.sh [--no-open]' >&2; exit 2; fi
case "${1-}" in
  --no-open) TASK_INSTALL_OPEN=false ;;
  '') ;;
  *) echo '用法：install.sh [--no-open]' >&2; exit 2 ;;
esac
if pgrep -f '/米盘.app/Contents/MacOS/米盘' >/dev/null; then
  echo '请先从菜单栏退出米盘，再运行安装。' >&2
  exit 1
fi
if [ ! -d "$ROOT/dist/米盘.app" ]; then "$ROOT/scripts/build.sh"; fi
mkdir -p "$HOME/Applications"
TARGET_APP="$HOME/Applications/米盘.app"
TASK_INSTALL_TEMP="$(mktemp -d "$HOME/Applications/.midrive-install.XXXXXX")"
cleanup() {
  if [ ! -d "$TARGET_APP" ] && [ -d "$TASK_INSTALL_TEMP/previous.app" ]; then
    mv "$TASK_INSTALL_TEMP/previous.app" "$TARGET_APP"
  fi
  rm -rf "$TASK_INSTALL_TEMP"
}
trap cleanup EXIT
ditto "$ROOT/dist/米盘.app" "$TASK_INSTALL_TEMP/米盘.app"
codesign --verify --deep --strict "$TASK_INSTALL_TEMP/米盘.app"
if [ -d "$TARGET_APP" ]; then mv "$TARGET_APP" "$TASK_INSTALL_TEMP/previous.app"; fi
mv "$TASK_INSTALL_TEMP/米盘.app" "$TARGET_APP"
mkdir -p "$HOME/.local/bin"
ln -sfn "$TARGET_APP/Contents/MacOS/mipan" "$HOME/.local/bin/mipan"
# Register the replaced bundle and its content-versioned icon with Launch Services.
touch "$TARGET_APP"
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$TARGET_APP"
if [ "$TASK_INSTALL_OPEN" = true ]; then open "$TARGET_APP"; fi
