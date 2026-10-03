#!/bin/sh
# This fork is installed from its local checkout, including all embedded assets.
# Clone https://github.com/haohao3k/piggery and run ./install.sh there.
set -eu

checkout=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ ! -f "$checkout/go.mod" ] || [ ! -f "$checkout/scripts/local-dev.sh" ]; then
    echo "piggery install: clone https://github.com/haohao3k/piggery, check out the development branch, and run ./install.sh there; piping a release installer is not supported by this fork" >&2
    exit 1
fi
exec "$checkout/scripts/local-dev.sh" apply "$@"
