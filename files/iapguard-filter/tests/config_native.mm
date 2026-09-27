#import "IAPGuardConfig.h"
#include <assert.h>
#include <sys/stat.h>

static NSString *testPath;
@interface IAPGuardConfig (TestPath)
- (NSString *)resolveConfigPath;
@end
@interface TestConfig : IAPGuardConfig
@end
@implementation TestConfig
- (NSString *)resolveConfigPath { return testPath; }
@end

static void writeQuota(NSInteger quantity) {
    assert(([@{@"enabled": @YES, @"allowedPriceQuotas": @{@"0.29": @(quantity)}}
        writeToFile:testPath atomically:YES]));
}

int main(int argc, const char **argv) {
    @autoreleasepool {
        assert(argc == 2);
        NSString *directory = [NSString stringWithUTF8String:argv[1]];
        testPath = [directory stringByAppendingPathComponent:@"policy.plist"];
        writeQuota(2);
        TestConfig *config = [[TestConfig alloc] init];
        assert([config isPriceAllowed:@"0.29"]);
        // Real filesystem denial: atomic replacement needs a writable parent.
        assert(chmod(directory.fileSystemRepresentation, 0500) == 0);
        NSInteger remaining = -1;
        BOOL used = NO;
        BOOL allowed = [config consumeAllowanceForPrice:@"0.29"
            remainingQuotaAfterConsume:&remaining usedQuota:&used];
        assert(chmod(directory.fileSystemRepresentation, 0700) == 0);
        assert(!allowed && !used && remaining == 0);
        assert(![config isPriceAllowed:@"0.29"]);
        NSDictionary *disk = [NSDictionary dictionaryWithContentsOfFile:testPath];
        assert([disk[@"allowedPriceQuotas"][@"0.29"] integerValue] == 2);

        // A newly prepared session can persist decrements, including the last one.
        writeQuota(2);
        config = [[TestConfig alloc] init];
        assert(![config isPriceAllowed:@"0.99"]);
        for (NSInteger expected = 1; expected >= 0; --expected) {
            assert([config consumeAllowanceForPrice:@"0.29"
                remainingQuotaAfterConsume:&remaining usedQuota:&used]);
            assert(used && remaining == expected);
            disk = [NSDictionary dictionaryWithContentsOfFile:testPath];
            assert([disk[@"allowedPriceQuotas"][@"0.29"] integerValue] == expected);
        }
        assert(![config consumeAllowanceForPrice:@"0.29"
            remainingQuotaAfterConsume:&remaining usedQuota:&used]);
        assert(!used && remaining == 0);
        config = [[TestConfig alloc] init];
        assert(![config isPriceAllowed:@"0.29"]); // Exhaustion survives process reload.
        NSString *actual = [[[IAPGuardConfig alloc] init] resolveConfigPath];
        assert([actual isEqual:[NSHomeDirectory() stringByAppendingPathComponent:
            @"Library/Preferences/com.iapguard.runtime.plist"]]);
        puts("PASS: real atomic write failure denies purchase; durable quota consumption and exhaustion; app-container path");
    }
}
