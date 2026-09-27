# IAPGuard Development Notes

## Honor / mr cancellation compatibility

The tested `mr.dylib` registers only its `InsideAppStore` observer with StoreKit.
Its failed-transaction branch finishes the transaction without notifying the game's
original observer. For Honor of Kings only, and only when that class comes from
`mr.dylib`, IAPGuard preserves the original handler and relays genuine StoreKit
user-cancellation events to the captured game observers on the main queue.
The relay excludes its source and deduplicates each transaction. Success, restored
transactions and other failures are not relayed. Quotas are never refunded.
Repeated finishing of relayed cancellations or locally denied transactions does
not send the same transaction to StoreKit twice.

Run `python3 tests/test_cancellation_relay_native.py` for the isolated native
regression test. Real-device coexistence must still be checked after plugin or
game updates; this is a compatibility path for the tested game and plugin.

## Runtime model

IAPGuard has two runtime inputs:

1. StoreKit 1 product metadata returned by the target app's normal `SKProductsRequest` flow.
2. An app-container runtime plist that lists the currently allowed prices.

The tweak does not need pre-collected product IDs. Every injected app process keeps its own in-memory product price map:

```text
productIdentifier -> product.price.stringValue
```

Purchase decisions use that runtime price map plus the app-container `allowedPriceQuotas` plist. Legacy `allowedPrices` remains supported when no quota dictionary exists.

## Runtime plist

Runtime config path relative to `NSHomeDirectory()` (the game data container):

```text
Library/Preferences/com.iapguard.runtime.plist
```

The plugin reads and atomically updates this file inside its own app container. The controller must resolve the game’s current data container (for example, XXTouch `app.data_path(bundle_id)`) and write the same file before launching the game. Do not apply `jbroot()` to this path.

Migration: the old `.jbroot-*/var/mobile/Library/Preferences` policy is no longer read. Stop the game, change the device profile’s `policy_path` to the game-container path, and write a fresh order policy. Container UUIDs may change after reinstall. The `.deb` intentionally contains no runtime policy; keep the repository plist as a sample only.

Format:

```xml
<dict>
  <key>enabled</key>
  <true/>
  <key>allowedPriceQuotas</key>
  <dict>
    <key>0.99</key>
    <integer>5</integer>
  </dict>
</dict>
```

Rules:

- `enabled=false`: allow all purchases.
- `enabled=true` with `allowedPriceQuotas`: allow only prices whose remaining count is greater than `0`, then decrement and write back.
- `enabled=true` without `allowedPriceQuotas`: fall back to legacy `allowedPrices`.
- Missing plist, empty quota map, unknown product price, non-matching price, or quota `0`: deny.
- A quota decrement is allowed only after an atomic write and matching readback. Write failure denies the purchase and clears in-memory quotas. Cancellation does not refund a consumed attempt.
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
cd files/iapguard-filter
make clean package FINALPACKAGE=1
```

For a diagnostic package add `IAPGUARD_DIAGNOSTICS=1`; release builds omit the boundary logs. The diagnostic filter used for Honor of Kings contains only `com.levelinfinite.sgameGlobal`. These tests do not establish that StoreKit callbacks or another injected plugin behave correctly on a real phone.

The generated roothide deb is written to:

```text
files/iapguard-filter/packages/
```

Find the latest package:

```sh
ls -t files/iapguard-filter/packages/*iphoneos-arm64e.deb | head -1
```

## Verification commands

From the repository root:

```sh
sh files/iapguard-filter/tests/test_config.sh
sh files/iapguard-filter/tests/test_roothide_sources.sh
sh files/iapguard-filter/tests/test_manager_sources.sh
sh files/iapguard-filter/tests/test_alert_sources.sh
sh files/iapguard-filter/tests/test_dynamic_prices_sources.sh
plutil -lint files/iapguard-filter/zzzIAPGuard.plist files/iapguard-filter/com.iapguard.runtime.plist
```

After packaging:

```sh
DEB=$(ls -t files/iapguard-filter/packages/*iphoneos-arm64e.deb | head -1)
sh files/iapguard-filter/tests/test_roothide_package.sh "$DEB"
python3 files/iapguard-filter/tests/test_config_native.py
```
