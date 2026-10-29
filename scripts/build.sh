#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
APP="$ROOT/dist/米盘.app"
VERSION="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' macos/Info.plist)"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X github.com/champlus/xiaomi-cloud-drive/internal/app.Version=$VERSION" -o "$APP/Contents/MacOS/midrive-service" ./cmd/midrive
CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X github.com/champlus/xiaomi-cloud-drive/internal/app.Version=$VERSION" -o "$APP/Contents/MacOS/mipan" ./cmd/mipan
swiftc -swift-version 5 -O -framework AppKit -framework WebKit -framework NetFS -framework Security macos/WebSession.swift macos/Transfer.swift macos/SettingsComponents.swift macos/TransfersView.swift macos/CredentialBroker.swift macos/WebSessionStore.swift macos/main.swift macos/SettingsView.swift macos/LocalServiceName.swift -o "$APP/Contents/MacOS/米盘"
rm -f "$APP/Contents/Resources/AppIcon.icns"
cp macos/Info.plist "$APP/Contents/Info.plist"
cp macos/AppIcon.png "$APP/Contents/Resources/AppIcon.png"
swift scripts/build-icon.swift macos/AppIcon.png .build/AppIcon.iconset
ICON_NAME="$(python3 -c 'import hashlib; from pathlib import Path; print("MiPanIcon-"+hashlib.sha256(Path("macos/AppIcon.png").read_bytes()+Path("scripts/build-icon.swift").read_bytes()).hexdigest()[:12])')"
rm -f "$APP/Contents/Resources/MiCloudIcon.icns" "$APP/Contents/Resources/"MiPanIcon-*.icns
iconutil -c icns .build/AppIcon.iconset -o "$APP/Contents/Resources/$ICON_NAME.icns"
/usr/libexec/PlistBuddy -c "Set :CFBundleIconFile $ICON_NAME" "$APP/Contents/Info.plist"
python3 "$ROOT/scripts/build-credential-helper.py" "$APP/Contents/MacOS/credential-helper"
python3 "$ROOT/scripts/sign-app.py" "$APP"
/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist"
