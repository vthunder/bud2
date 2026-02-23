#!/bin/sh
cd ~/src/bud2
export CGO_CFLAGS="-Wno-deprecated-declarations"
go build -o bin/bud ./cmd/bud
go build -o bin/bud-state ./cmd/bud-state
go build -o bin/efficient-notion-mcp ./cmd/efficient-notion-mcp
go build -o bin/compress-episodes ./cmd/compress-episodes
go build -o bin/compress-traces ./cmd/compress-traces
go build -o bin/consolidate ./cmd/consolidate
cd -
