#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
SRC="$PROJECT_DIR/src/Tweak.xm"

grep -F 'IAPGuardDeniedTransactionArray' "$SRC" >/dev/null || {
  echo "missing manager-style denied transaction array" >&2
  exit 1
}

grep -F -- '- (NSArray *)transactions' "$SRC" >/dev/null || {
  echo "missing transactions hook" >&2
  exit 1
}

grep -F 'IAPGuardMergedTransactions' "$SRC" >/dev/null || {
  echo "missing merged transactions helper" >&2
  exit 1
}

grep -F 'IAPGuardRemoveDeniedTransaction(transaction)' "$SRC" >/dev/null || {
  echo "finishTransaction does not remove denied transaction from manager array" >&2
  exit 1
}

echo "manager source regression test passed"
