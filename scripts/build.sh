#!/bin/sh
cd ~/src/bud2
go build -o bin/bud ./cmd/bud && go build -o bin/bud-mcp ./cmd/bud-mcp
cd -
