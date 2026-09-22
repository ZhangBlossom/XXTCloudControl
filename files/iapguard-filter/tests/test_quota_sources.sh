#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
SRC="$PROJECT_DIR/src/Tweak.xm"
CONFIG_SRC="$PROJECT_DIR/src/IAPGuardConfig.mm"
HEADER="$PROJECT_DIR/src/IAPGuardConfig.h"

for pattern in \
  'allowedPriceQuotas' \
  'hasAllowedPriceQuotas' \
  'consumeAllowanceForPrice' \
  'remainingQuotaAfterConsume' \
  'writeAllowedPriceQuotas' \
  'remainingQuotaDisplayStringForPrice'
  do
    grep -F "$pattern" "$CONFIG_SRC" "$HEADER" >/dev/null || {
      echo "missing quota config pattern: $pattern" >&2
      exit 1
    }
  done

grep -F 'consumeAllowanceForPrice:priceString' "$SRC" >/dev/null || {
  echo "addPayment does not consume quota before allowing" >&2
  exit 1
}

grep -F 'IAPGuardPresentDeniedAlert(productIdentifier, priceString, remainingQuotaString)' "$SRC" >/dev/null || {
  echo "deny alert does not include quota state" >&2
  exit 1
}

grep -F '剩余次数' "$SRC" >/dev/null || {
  echo "alert does not show remaining quota" >&2
  exit 1
}

echo "quota source regression test passed"
