#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
APP="$ROOT/dist/米盘.app"
VERSION="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist")"
ARCH="$(uname -m)"
ARCHIVE="MiPan-v${VERSION}-macos-${ARCH}.zip"
CLI="mipan-macos-${ARCH}"
codesign --verify --deep --strict "$APP"
rm -f "$ROOT/dist/$ARCHIVE"
python3 - "$APP" "$ROOT/dist/$ARCHIVE" <<'PYZIP'
from pathlib import Path
import sys
import zipfile
app=Path(sys.argv[1])
with zipfile.ZipFile(sys.argv[2], 'w', compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
    archive.write(app, app.name+'/')
    for item in sorted(app.rglob('*')):
        archive.write(item, item.relative_to(app.parent).as_posix())
PYZIP
cp "$APP/Contents/MacOS/mipan" "$ROOT/dist/$CLI"
cd "$ROOT/dist"
shasum -a 256 "$ARCHIVE" "$CLI" > SHA256SUMS.txt
