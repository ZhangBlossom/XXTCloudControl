#import <Foundation/Foundation.h>

// Enable only for a diagnostic package: make ... IAPGUARD_DIAGNOSTICS=1.
#ifndef IAPGUARD_DIAGNOSTICS
#define IAPGUARD_DIAGNOSTICS 0
#endif
#if IAPGUARD_DIAGNOSTICS
#define IAPGuardTrace(format, ...) NSLog(@"[IAPGuardDiag] " format, ##__VA_ARGS__)
#else
#define IAPGuardTrace(...) do {} while (0)
#endif
