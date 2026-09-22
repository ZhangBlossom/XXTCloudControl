#import <Foundation/Foundation.h>

NS_ASSUME_NONNULL_BEGIN

@interface IAPGuardConfig : NSObject

+ (instancetype)sharedConfig;
- (void)reloadIfNeeded;
- (BOOL)isPriceAllowed:(NSString *)priceString;
- (BOOL)consumeAllowanceForPrice:(NSString *)priceString remainingQuotaAfterConsume:(NSInteger *)remainingQuotaAfterConsume usedQuota:(BOOL *)usedQuota;
- (NSSet<NSString *> *)allowedPricesSnapshot;
- (NSString *)allowedPricesDisplayString;
- (NSInteger)remainingQuotaForPrice:(NSString *)priceString;
- (NSString *)remainingQuotaDisplayStringForPrice:(NSString *)priceString;

@end

NS_ASSUME_NONNULL_END
