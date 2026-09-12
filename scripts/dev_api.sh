#!/usr/bin/env bash
set -euo pipefail

# Compatibility entrypoint for existing local workflows. The backend is Go-only.
exec "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/dev_go_api.sh" "$@"
