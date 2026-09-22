#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
SRC="$PROJECT_DIR/src/Tweak.xm"
CONFIG_SRC="$PROJECT_DIR/src/IAPGuardConfig.mm"
HEADER="$PROJECT_DIR/src/IAPGuardConfig.h"

for pattern in \
  'IAPGuardProductPrices' \
  'IAPGuardRecordProduct' \
  'IAPGuardPriceForProductIdentifier' \
  'productsRequest:didReceiveResponse:' \
  'product.price.stringValue' \
  'product.productIdentifier' \
  'IAPGuardPresentDeniedAlert(productIdentifier, priceString, remainingQuotaString)'
  do
    grep -F "$pattern" "$SRC" >/dev/null || {
      echo "missing dynamic price source pattern: $pattern" >&2
      exit 1
    }
  done

grep -F 'kIAPGuardRootFSConfigPath = @"/var/mobile/Library/Preferences/com.iapguard.runtime.plist"' "$CONFIG_SRC" >/dev/null || {
  echo "config path is not global runtime plist" >&2
  exit 1
}

grep -F 'allowedPrices' "$CONFIG_SRC" >/dev/null || {
  echo "config source does not parse allowedPrices" >&2
  exit 1
}

grep -F -- '- (BOOL)isPriceAllowed:(NSString *)priceString;' "$HEADER" >/dev/null || {
  echo "config header missing isPriceAllowed" >&2
  exit 1
}

grep -F -- '- (NSSet<NSString *> *)allowedPricesSnapshot;' "$HEADER" >/dev/null || {
  echo "config header missing allowedPricesSnapshot" >&2
  exit 1
}

grep -F -- '- (NSString *)allowedPricesDisplayString;' "$HEADER" >/dev/null || {
  echo "config header missing allowedPricesDisplayString" >&2
  exit 1
}

if grep -F 'isProductAllowed' "$SRC" "$CONFIG_SRC" "$HEADER" >/dev/null; then
  echo "old product whitelist API still used" >&2
  exit 1
fi

echo "dynamic price source regression test passed"
