# IAPGuard Development Notes

## Runtime model

IAPGuard has two runtime inputs:

1. StoreKit 1 product metadata returned by the target app's normal `SKProductsRequest` flow.
2. A single global runtime plist that lists the currently allowed prices.

The tweak does not need pre-collected product IDs. Every injected app process keeps its own in-memory product price map:

```text
productIdentifier -> product.price.stringValue
```

Purchase decisions use that runtime price map plus the global `allowedPriceQuotas` plist. Legacy `allowedPrices` remains supported when no quota dictionary exists.

## Runtime plist

Logical path:

```text
/var/mobile/Library/Preferences/com.iapguard.runtime.plist
```

roothide path resolution is handled in code with `jbroot()`. On device the resolved path looks like:

```text
/var/containers/Bundle/Application/.jbroot-XXXX/var/mobile/Library/Preferences/com.iapguard.runtime.plist
```

Format:

```xml
<dict>
  <key>enabled</key>
  <true/>
  <key>allowedPrices</key>
  <array>
    <string>0.99</string>
  </array>
</dict>
```

Rules:

- `enabled=false`: allow all purchases.
- `enabled=true` with `allowedPriceQuotas`: allow only prices whose remaining count is greater than `0`, then decrement and write back.
- `enabled=true` without `allowedPriceQuotas`: fall back to legacy `allowedPrices`.
- Missing plist, empty quota map, unknown product price, non-matching price, or quota `0`: deny.
- The config loader checks the plist modification time before purchase decisions and reloads when changed.

## Load order

The tweak target is named `zzzIAPGuard` so the installed files sort late in `/Library/MobileSubstrate/DynamicLibraries/`:

```text
zzzIAPGuard.dylib
zzzIAPGuard.plist
```

For common MobileSubstrate-style loaders, later-loaded hooks sit on the outside of the hook chain. This makes the purchase gate run before earlier hooks when `-[SKPaymentQueue addPayment:]` is called.

## StoreKit hooks

Main hook points:

- `SKRequest setDelegate:` wraps `SKProductsRequest` delegates with `IAPGuardProductsRequestDelegateProxy`.
- `productsRequest:didReceiveResponse:` records `SKProduct.productIdentifier` and `SKProduct.price.stringValue`, then forwards the original callback.
- `SKProductsResponse products` also records products as a fallback if the app accesses the response directly.
- `SKPaymentQueue addPayment:` gates purchase attempts by recorded price and consumes one quota count before forwarding allowed purchases.
- `SKPaymentQueue transactions` merges locally denied fake transactions into StoreKit's transaction list.
- `SKPaymentQueue finishTransaction:` consumes locally denied fake transactions without forwarding them to StoreKit.

## Failure behavior

Denied purchases create `IAPGuardFailedTransaction`, notify registered StoreKit observers via `paymentQueue:updatedTransactions:`, and display an alert containing:

- product ID
- current recorded price, or `未知`
- allowed price list / quota map
- remaining quota

The original `addPayment:` is not called for denied purchases.

## Manual package commands

From the repository root:

```sh
cd /Users/qzytwway/Documents/game_top_up/plugin/iapguard-filter
make clean package FINALPACKAGE=1
```

The generated roothide deb is written to:

```text
/Users/qzytwway/Documents/game_top_up/plugin/iapguard-filter/packages/
```

Find the latest package:

```sh
ls -t /Users/qzytwway/Documents/game_top_up/plugin/iapguard-filter/packages/*iphoneos-arm64e.deb | head -1
```

## Verification commands

From the repository root:

```sh
./iapguard-filter/tests/test_config.sh
./iapguard-filter/tests/test_roothide_sources.sh
./iapguard-filter/tests/test_manager_sources.sh
./iapguard-filter/tests/test_alert_sources.sh
./iapguard-filter/tests/test_dynamic_prices_sources.sh
plutil -lint iapguard-filter/zzzIAPGuard.plist iapguard-filter/com.iapguard.runtime.plist iapguard-filter/layout/var/mobile/Library/Preferences/com.iapguard.runtime.plist
```

After packaging:

```sh
DEB=$(ls -t iapguard-filter/packages/*iphoneos-arm64e.deb | head -1)
./iapguard-filter/tests/test_roothide_package.sh "$DEB"
dpkg-deb -f "$DEB" Package Version Architecture
```
