#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

grep -F 'THEOS_PACKAGE_SCHEME = roothide' "$PROJECT_DIR/Makefile" >/dev/null || {
  echo "Makefile is not configured for roothide" >&2
  exit 1
}

grep -F 'TWEAK_NAME = zzzIAPGuard' "$PROJECT_DIR/Makefile" >/dev/null || {
  echo "tweak is not named for late hook loading" >&2
  exit 1
}

grep -F '#import <roothide.h>' "$PROJECT_DIR/src/IAPGuardConfig.mm" >/dev/null || {
  echo "config loader does not import roothide.h" >&2
  exit 1
}

grep -F 'jbroot(kIAPGuardRootFSConfigPath)' "$PROJECT_DIR/src/IAPGuardConfig.mm" >/dev/null || {
  echo "config loader does not resolve config path through jbroot()" >&2
  exit 1
}

grep -F 'transactionReceipt' "$PROJECT_DIR/src/IAPGuardFailedTransaction.mm" >/dev/null || {
  echo "fake transaction does not implement transactionReceipt" >&2
  exit 1
}

grep -F 'downloads' "$PROJECT_DIR/src/IAPGuardFailedTransaction.mm" >/dev/null || {
  echo "fake transaction does not implement downloads" >&2
  exit 1
}

echo "roothide source regression test passed"
