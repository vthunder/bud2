#!/bin/sh
cd ~/src/bud2
go build -o bin/bud ./cmd/bud; go build -o bin/bud-mcp ./cmd/bud-mcp; go build -o bin/bud-state ./cmd/bud-state; go build -o bin/efficient-notion-mcp ./cmd/efficient-notion-mcp
cd -
