#!/bin/sh
# Build, install, and verify the local Piggery checkout.
#
# The Python implementation owns all policy and is deliberately invoked with an absolute
# path so running this entrypoint from another directory cannot change which checkout is built.
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$script_dir/local-dev.py" "$@"
