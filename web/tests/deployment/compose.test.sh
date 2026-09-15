#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../../.." && pwd)

# This is intentionally static: Compose rendering would require a local Docker
# installation and cannot prove the image/runtime boundary. check-composition
# verifies both local topologies, immutable image defaults, and service policy.
exec bun "$repo_root/web/scripts/check-composition.ts"
