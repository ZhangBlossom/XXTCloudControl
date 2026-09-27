#import "IAPGuardCancellationRelay.h"
#include <assert.h>

@interface TestTransaction : NSObject
@property SKPaymentTransactionState transactionState;
@property(nonatomic, strong) NSError *error;
@end
@implementation TestTransaction
@end

@interface TestObserver : NSObject
@property NSUInteger calls;
@property(nonatomic, strong) NSArray *received;
@end
@implementation TestObserver
- (void)paymentQueue:(SKPaymentQueue *)queue updatedTransactions:(NSArray *)transactions {
    self.calls++;
    self.received = transactions;
}
@end

static TestTransaction *transaction(SKPaymentTransactionState state, NSString *domain, NSInteger code) {
    TestTransaction *result = [TestTransaction new];
    result.transactionState = state;
    result.error = [NSError errorWithDomain:domain code:code userInfo:nil];
    return result;
}

int main() {
    @autoreleasepool {
        TestObserver *source = [TestObserver new], *game = [TestObserver new];
        NSArray *observers = @[source, game, [NSObject new]];
        TestTransaction *cancel = transaction(SKPaymentTransactionStateFailed, SKErrorDomain, SKErrorPaymentCancelled);
        TestTransaction *success = transaction(SKPaymentTransactionStatePurchased, SKErrorDomain, SKErrorPaymentCancelled);
        TestTransaction *otherFailure = transaction(SKPaymentTransactionStateFailed, SKErrorDomain, SKErrorPaymentNotAllowed);
        TestTransaction *otherDomain = transaction(SKPaymentTransactionStateFailed, @"not.StoreKit", SKErrorPaymentCancelled);
        IAPGuardRelayCancelledPayments(nil, @[success, otherFailure, otherDomain], observers, source);
        assert(game.calls == 0 && source.calls == 0);
        IAPGuardRelayCancelledPayments(nil, @[success, cancel, otherFailure, cancel, otherDomain], observers, source);
        assert(game.calls == 1 && source.calls == 0);
        assert(game.received.count == 1 && game.received[0] == cancel);
        IAPGuardRelayCancelledPayments(nil, @[cancel], observers, source);
        assert(game.calls == 1 && source.calls == 0);
        TestTransaction *second = transaction(SKPaymentTransactionStateFailed, SKErrorDomain, SKErrorPaymentCancelled);
        IAPGuardRelayCancelledPayments(nil, @[cancel, second, otherFailure], observers, source);
        assert(game.calls == 2 && source.calls == 0);
        assert(game.received.count == 1 && game.received[0] == second);
        IAPGuardRelayCancelledPayments(nil, @[cancel, second], observers, source);
        assert(game.calls == 2);
        puts("PASS: actual relay filters genuine cancellations, excludes source, deduplicates mixed batches and repeated notifications");
    }
}
