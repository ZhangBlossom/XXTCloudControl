#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
SRC="$PROJECT_DIR/src/Tweak.xm"
CONFIG_SRC="$PROJECT_DIR/src/IAPGuardConfig.mm"
HEADER="$PROJECT_DIR/src/IAPGuardConfig.h"

for noisy in \
  'debugLogging' \
  'isDebugLoggingEnabled' \
  'IAPGuardDebugLog' \
  'NSLog' \
  '[IAPGuard]'
  do
    if grep -F "$noisy" "$SRC" "$CONFIG_SRC" >/dev/null; then
      echo "release logging still present: $noisy" >&2
      exit 1
    fi
  done

if grep -F 'debugLogging' "$HEADER" >/dev/null || grep -F 'isDebugLoggingEnabled' "$HEADER" >/dev/null; then
  echo "release logging API still present in header" >&2
  exit 1
fi

echo "release logging source regression test passed"
