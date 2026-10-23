#!/bin/sh
set -eu
base=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$base"
export GOMAXPROCS=2
go test -count=1 ./...
go run . "$@"
