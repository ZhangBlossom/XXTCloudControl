#import <Foundation/Foundation.h>
#import <StoreKit/StoreKit.h>

// Only for the verified mr/InsideAppStore compatibility path. Never forwards success.
void IAPGuardRelayCancelledPayments(SKPaymentQueue *queue, NSArray *transactions,
                                   NSArray *observers, id sourceObserver);
