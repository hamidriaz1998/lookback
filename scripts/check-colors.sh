#!/usr/bin/env bash
# Fails when a Go file outside internal/theme uses a color literal (UI guide section 5).
# Test files and the spike programs are exempt.
set -euo pipefail
cd "$(dirname "$0")/.."

hits=$(grep -rnF --include='*.go' -e 'ui.Hex(' -e 'ui.RGB(' . \
  | grep -v -e '^\./internal/theme/' -e '^\./cmd/spike-' -e '_test\.go:' || true)

if [ -n "$hits" ]; then
  echo "Color literals outside internal/theme (use c.Theme() instead):" >&2
  echo "$hits" >&2
  exit 1
fi
echo "check-colors: no color literals outside internal/theme"
