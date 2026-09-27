#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
CONFIG="$PROJECT_DIR/com.iapguard.runtime.plist"

for file in "$CONFIG"; do
  [ -f "$file" ] || {
    echo "missing runtime config: $file" >&2
    exit 1
  }

  plutil -lint "$file" >/dev/null
  plutil -p "$file" | grep -F '"allowedPriceQuotas"' >/dev/null || {
    echo "missing allowedPriceQuotas in $file" >&2
    exit 1
  }
  plutil -p "$file" | grep -F '"0.99"' >/dev/null || {
    echo "missing default quota price 0.99 in $file" >&2
    exit 1
  }
  plutil -p "$file" | grep -E '"0\.99" => [0-9]+' >/dev/null || {
    echo "missing numeric default quota for 0.99 in $file" >&2
    exit 1
  }

  if plutil -p "$file" | grep -F '"debugLogging"' >/dev/null; then
    echo "debugLogging should not be present in release config $file" >&2
    exit 1
  fi

  if plutil -p "$file" | grep -F '"allowedProductIds"' >/dev/null; then
    echo "old allowedProductIds key still present in $file" >&2
    exit 1
  fi
done

echo "sample price quota config test passed (not an installed policy)"
