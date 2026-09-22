#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
SRC="$PROJECT_DIR/src/Tweak.xm"

grep -F '#import <UIKit/UIKit.h>' "$SRC" >/dev/null || {
  echo "missing UIKit import" >&2
  exit 1
}

grep -F 'IAPGuardPresentDeniedAlert' "$SRC" >/dev/null || {
  echo "missing denied alert presenter" >&2
  exit 1
}

grep -F 'UIAlertController' "$SRC" >/dev/null || {
  echo "missing UIAlertController usage" >&2
  exit 1
}

grep -F '购买已拦截' "$SRC" >/dev/null || {
  echo "missing alert title" >&2
  exit 1
}

grep -F '当前价格' "$SRC" >/dev/null || {
  echo "missing current price in alert" >&2
  exit 1
}

grep -F '允许价格' "$SRC" >/dev/null || {
  echo "missing allowed prices in alert" >&2
  exit 1
}

grep -F '剩余次数' "$SRC" >/dev/null || {
  echo "missing remaining quota in alert" >&2
  exit 1
}

grep -F 'IAPGuardPresentDeniedAlert(productIdentifier, priceString, remainingQuotaString)' "$SRC" >/dev/null || {
  echo "deny branch does not present price-aware alert" >&2
  exit 1
}

echo "alert source regression test passed"
