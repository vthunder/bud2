#!/bin/sh
cd ~/src/bud2
export CGO_CFLAGS="-Wno-deprecated-declarations"
go build -o bin/bud ./cmd/bud
go build -o bin/efficient-notion-mcp ./cmd/efficient-notion-mcp
cd -
