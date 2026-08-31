#!/bin/bash

# The echo fixture is a buf module of its own, the way pkg/sensors/proto is,
# so it does not enter the workspace at the repo root. It lives under testdata
# because it is a test fixture: `go build ./...` and `go vet ./...` skip it,
# and only p2p/rpc's tests import it.

set -euo pipefail

go install github.com/bufbuild/buf/cmd/buf@latest
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install connectrpc.com/connect/cmd/protoc-gen-connect-go@latest

buf generate
