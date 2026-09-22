#!/bin/sh
set -eu

DEB="${1:-}"
[ -n "$DEB" ] || {
  echo "usage: $0 path/to/package.deb" >&2
  exit 1
}
[ -f "$DEB" ] || {
  echo "missing package: $DEB" >&2
  exit 1
}

dpkg-deb -f "$DEB" Architecture | grep -Fx "iphoneos-arm64e" >/dev/null || {
  echo "package architecture is not roothide iphoneos-arm64e" >&2
  exit 1
}

dpkg-deb -c "$DEB" | grep -F "Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.dylib" >/dev/null || {
  echo "missing late-loading substrate dylib" >&2
  exit 1
}

dpkg-deb -c "$DEB" | grep -F "Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.plist" >/dev/null || {
  echo "missing late-loading substrate filter plist" >&2
  exit 1
}

dpkg-deb -c "$DEB" | grep -F "var/mobile/Library/Preferences/com.iapguard.runtime.plist" >/dev/null || {
  echo "missing runtime price config plist" >&2
  exit 1
}

if dpkg-deb -c "$DEB" | grep -F "var/mobile/Library/Preferences/com.iapguard.allowed-products.plist" >/dev/null; then
  echo "old product whitelist plist is still packaged" >&2
  exit 1
fi

echo "roothide package test passed"
