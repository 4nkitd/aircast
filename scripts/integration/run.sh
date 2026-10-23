#!/bin/sh
set -eu
base=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "$(uname -s)" != Linux ]; then
    echo "Aircast integration tests require the disposable Linux VM, not the Mac desktop." >&2
    exit 2
fi
export GOMAXPROCS=2
exec unshare --net --pid --mount --mount-proc --fork --kill-child=SIGTERM \
    uv run --no-project "$base/run.py" "$@"
