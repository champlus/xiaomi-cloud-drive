#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
mkdir -p .build
go test -race ./...
go vet ./...
swiftc macos/LocalServiceName.swift tests/NativeTests.swift -o .build/native-tests
.build/native-tests
