# IAPGuard

IAPGuard is a StoreKit 1 MobileSubstrate tweak that gates purchases by runtime product prices and per-price quotas.

## Behavior

- The tweak records each app's StoreKit product table at runtime as `productIdentifier -> price`.
- The allowed prices come from a runtime plist inside the injected app’s data container.
- If `enabled` is `false`, all `SKPayment` purchases are allowed.
- If `enabled` is `true`, products are allowed by `allowedPriceQuotas` when present, otherwise by legacy `allowedPrices`.
- With `allowedPriceQuotas`, a price is allowed only while its remaining count is greater than `0`; the count is decremented when `addPayment:` is allowed.
- Products with unknown prices, missing quota entries, or exhausted quotas are denied while enabled.
- Denied purchases do not call original `-[SKPaymentQueue addPayment:]`; they notify the app's registered StoreKit observers with a failed transaction using `SKErrorDomain` / `SKErrorPaymentNotAllowed` and show a UI alert.

## Runtime config

Runtime config path relative to `NSHomeDirectory()` (the game data container):

```text
Library/Preferences/com.iapguard.runtime.plist
```

The plugin reads and atomically updates this file inside its own app container. The controller must resolve the game’s current data container (for example, XXTouch `app.data_path(bundle_id)`) and write the same file before launching the game. Do not apply `jbroot()` to this path.

Migration: the old `.jbroot-*/var/mobile/Library/Preferences` policy is no longer read. Stop the game, change the device profile’s `policy_path` to the game-container path, and write a fresh order policy. Container UUIDs may change after reinstall. The `.deb` intentionally contains no runtime policy; keep the repository plist as a sample only.

Config format:

```xml
<dict>
  <key>enabled</key>
  <true/>
  <key>allowedPriceQuotas</key>
  <dict>
    <key>0.99</key>
    <integer>5</integer>
    <key>4.99</key>
    <integer>2</integer>
  </dict>
</dict>
```

Before each purchase decision, IAPGuard checks the config file modification time and reloads it if changed. When a quota-controlled purchase is allowed, the remaining count must be atomically written and read back successfully before forwarding payment. Write failure denies the purchase and clears in-memory quotas. Counts represent admitted purchase attempts, not successful payments; cancellation does not refund quota.

## tidevice flow

Use your controller to overwrite the runtime plist before launching the app:

```sh
# 1. overwrite com.iapguard.runtime.plist with the prices for this run
# 2. launch the target app
tidevice launch com.example.app
```

All apps read the same plist format. Product IDs do not need to be preconfigured because each app's StoreKit response provides its own product IDs and prices. To allow five `0.99` purchases, set `allowedPriceQuotas["0.99"] = 5` before launching the app.

## Load order

The tweak target is named `zzzIAPGuard` so the installed files sort late in `/Library/MobileSubstrate/DynamicLibraries/`:

```text
zzzIAPGuard.dylib
zzzIAPGuard.plist
```

For common MobileSubstrate-style loaders, later-loaded hooks sit on the outside of the hook chain. This makes the purchase gate run before earlier hooks when `-[SKPaymentQueue addPayment:]` is called.

## Development

See `DEVELOPMENT.md` for hook details, runtime data flow, verification commands, and packaging notes.

## Manual packaging

From this directory:

```sh
make clean package FINALPACKAGE=1
```

The Makefile sets `THEOS_PACKAGE_SCHEME = roothide`, so the generated package uses roothide's `iphoneos-arm64e` package architecture.

Find the newest generated package:

```sh
ls -t packages/*iphoneos-arm64e.deb | head -1
```

Install the generated `.deb` on the device, then respring or restart the target app.
