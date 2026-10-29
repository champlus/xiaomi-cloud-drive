#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
APP="$ROOT/dist/米盘.app"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o "$APP/Contents/MacOS/midrive-service" ./cmd/midrive
CGO_ENABLED=1 go build -trimpath -ldflags='-s -w' -o "$APP/Contents/MacOS/mipan" ./cmd/mipan
swiftc -swift-version 5 -O -framework AppKit -framework WebKit -framework NetFS -framework Security macos/WebSession.swift macos/WebSessionStore.swift macos/main.swift macos/SettingsView.swift macos/LocalServiceName.swift -o "$APP/Contents/MacOS/米盘"
rm -f "$APP/Contents/Resources/AppIcon.icns"
cp macos/Info.plist "$APP/Contents/Info.plist"
cp macos/AppIcon.png "$APP/Contents/Resources/AppIcon.png"
if [ -f macos/AppIcon.icns ]; then cp macos/AppIcon.icns "$APP/Contents/Resources/MiCloudIcon.icns"; fi
python3 "$ROOT/scripts/sign-app.py" "$APP"
/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist"
