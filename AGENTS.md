Use TDD for cloud, WebDAV, persistence and CLI changes: add a behavior test, run it to observe the failure, then implement the change and run the relevant suite. Use `scripts/test.sh` for unit tests, race detection, static checks and the native service-name test. Real-account tests are opt-in with `MIDRIVE_LIVE=1` and must use isolated test folders.

Keep the UI native SwiftUI. Preserve the sidebar and card layout adapted from Lyli, including consistent icon, label and trailing-control columns. Use short labels and show details when relevant; do not add explanatory copy to every control or put all actions in an ellipsis menu. The app icon is the PNG supplied by the user.

The service is local-only. Keep cloud sessions in Keychain, never print cookies or local credentials, and do not change system hosts or routing. Finder uses MiCloud.local as the local server name and 小米云盘 as the volume name.

Read README.md and docs/CLI.md for supported behavior and current limits. Build with scripts/build.sh. To update an installed running development build, use scripts/reload-dev.py; it refuses to interrupt active transfers.
