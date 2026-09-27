#import "IAPGuardCancellationRelay.h"
#import "IAPGuardDiagnostics.h"
#import <objc/runtime.h>
#import <objc/message.h>

static char kCancellationRelayed;

void IAPGuardRelayCancelledPayments(SKPaymentQueue *queue, NSArray *transactions,
                                   NSArray *observers, id sourceObserver) {
    NSMutableArray *cancelled = [NSMutableArray array];
    for (SKPaymentTransaction *transaction in transactions) {
        if (transaction.transactionState != SKPaymentTransactionStateFailed ||
            ![transaction.error.domain isEqualToString:SKErrorDomain] ||
            transaction.error.code != SKErrorPaymentCancelled) continue;
        @synchronized (transaction) {
            if (objc_getAssociatedObject(transaction, &kCancellationRelayed)) continue;
            objc_setAssociatedObject(transaction, &kCancellationRelayed, @YES, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        }
        [cancelled addObject:transaction];
    }
    if (!cancelled.count) return;
    SEL callback = @selector(paymentQueue:updatedTransactions:);
    for (id observer in observers) {
        if (observer == sourceObserver || ![observer respondsToSelector:callback]) continue;
        @try {
            IAPGuardTrace(@"cancel_relay observer=%@ count=%lu", NSStringFromClass([observer class]), (unsigned long)cancelled.count);
            ((void (*)(id, SEL, SKPaymentQueue *, NSArray *))objc_msgSend)(observer, callback, queue, cancelled);
        } @catch (NSException *exception) {
            IAPGuardTrace(@"cancel_relay_exception name=%@", exception.name);
        }
    }
}
