#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
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
open "$TARGET_APP"
