#import <Foundation/Foundation.h>
#import <StoreKit/StoreKit.h>

NS_ASSUME_NONNULL_BEGIN

@interface IAPGuardFailedTransaction : SKPaymentTransaction

- (instancetype)initWithPayment:(SKPayment *)payment;

@property (nonatomic, strong, readonly) SKPayment *payment;
@property (nonatomic, strong, readonly) NSError *error;
@property (nonatomic, assign, readonly) SKPaymentTransactionState transactionState;
@property (nonatomic, copy, readonly) NSString *transactionIdentifier;
@property (nonatomic, strong, readonly) NSDate *transactionDate;
@property (nonatomic, strong, readonly, nullable) SKPaymentTransaction *originalTransaction;
@property (nonatomic, strong, readonly) NSData *transactionReceipt;
@property (nonatomic, strong, readonly) NSArray *downloads;

@end

NS_ASSUME_NONNULL_END
