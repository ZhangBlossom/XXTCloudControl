#import "IAPGuardFailedTransaction.h"

@implementation IAPGuardFailedTransaction {
    SKPayment *_payment;
    NSError *_error;
    NSString *_transactionIdentifier;
    NSDate *_transactionDate;
}

- (instancetype)initWithPayment:(SKPayment *)payment {
    self = [super init];
    if (self) {
        _payment = payment;
        _transactionDate = [NSDate date];
        _transactionIdentifier = [@"iapguard-denied-" stringByAppendingString:[[NSUUID UUID] UUIDString]];
        _error = [NSError errorWithDomain:SKErrorDomain
                                     code:SKErrorPaymentNotAllowed
                                 userInfo:@{NSLocalizedDescriptionKey: @"This product is not allowed to be purchased."}];
    }
    return self;
}

- (SKPayment *)payment {
    return _payment;
}

- (NSError *)error {
    return _error;
}

- (SKPaymentTransactionState)transactionState {
    return SKPaymentTransactionStateFailed;
}

- (NSString *)transactionIdentifier {
    return _transactionIdentifier;
}

- (NSDate *)transactionDate {
    return _transactionDate;
}

- (SKPaymentTransaction *)originalTransaction {
    return nil;
}

- (NSData *)transactionReceipt {
    return [NSData data];
}

- (NSArray *)downloads {
    return @[];
}

- (NSString *)description {
    return [NSString stringWithFormat:@"<IAPGuardFailedTransaction: %p product=%@ error=%@>", self, self.payment.productIdentifier, self.error];
}

@end
