#import "IAPGuardConfig.h"
#include <assert.h>
int main() { @autoreleasepool {
 NSInteger remaining=0; BOOL usedQuota=NO;
 NSString *p=[NSString stringWithUTF8String:getenv("IAPGUARD_TEST_POLICY")];
 assert(([@{@"enabled":@YES,@"allowedPriceQuotas":@{@"648":@5}} writeToFile:p atomically:YES]));
 IAPGuardConfig *c=[[IAPGuardConfig alloc] init];
 assert(![c isPriceAllowed:@"328"]); assert(![c isPriceAllowed:@"998"]);
 for(int i=4;i>=0;--i) { NSInteger left=-1;BOOL used=NO;assert([c consumeAllowanceForPrice:@"648" remainingQuotaAfterConsume:&left usedQuota:&used]);assert(left==i && used); }
 assert(![c consumeAllowanceForPrice:@"648" remainingQuotaAfterConsume:&remaining usedQuota:&usedQuota]);
 assert([[[NSDictionary dictionaryWithContentsOfFile:p] objectForKey:@"allowedPriceQuotas"][@"648"] integerValue]==0);
 assert(([@{@"enabled":@YES,@"allowedPriceQuotas":@{@"1.98":@2}} writeToFile:p atomically:YES]));
 [c reloadIfNeeded];
 assert(![c isPriceAllowed:@"648"]);
 assert(![c isPriceAllowed:@"1.97"]);assert(![c isPriceAllowed:@"1.99"]);
 assert([c consumeAllowanceForPrice:@"1.98" remainingQuotaAfterConsume:&remaining usedQuota:&usedQuota]);
 assert([c consumeAllowanceForPrice:@"1.98" remainingQuotaAfterConsume:&remaining usedQuota:&usedQuota]);
 assert(![c consumeAllowanceForPrice:@"1.98" remainingQuotaAfterConsume:&remaining usedQuota:&usedQuota]);
 puts("PASS: actual IAPGuardConfig source: exact prices, decimal quota, decrement persistence, exhaustion (macOS isolated path; no StoreKit hook)");
} }
