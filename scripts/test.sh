#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .build
go test -race ./...
go vet ./...
swiftc macos/LocalServiceName.swift tests/NativeTests.swift -o .build/native-tests
.build/native-tests

swiftc macos/WebSession.swift tests/SessionTests.swift -o .build/session-tests
.build/session-tests

python3 tests/reload_test.py

# Opt in to an isolated, synthetic item in the unlocked local login Keychain.
if [ "${MIDRIVE_KEYCHAIN_TEST:-0}" = 1 ]; then python3 tests/credential_helper_test.py; fi
