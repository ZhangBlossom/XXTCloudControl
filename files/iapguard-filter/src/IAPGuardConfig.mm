#import "IAPGuardConfig.h"
#import <roothide.h>

// Logical roothide path. jbroot() resolves this to the real .jbroot path on device.
static NSString * const kIAPGuardRootFSConfigPath = @"/var/mobile/Library/Preferences/com.iapguard.runtime.plist";
static NSInteger const kIAPGuardUnlimitedQuota = NSIntegerMax;

@interface IAPGuardConfig ()
@property (nonatomic, strong) NSDate *lastModificationDate;
@property (nonatomic, strong) NSSet<NSString *> *allowedPrices;
@property (nonatomic, strong) NSDictionary<NSString *, NSNumber *> *allowedPriceQuotas;
@property (nonatomic, assign) BOOL hasAllowedPriceQuotas;
@property (nonatomic, copy) NSString *resolvedConfigPath;
@property (nonatomic, assign, getter=isEnabled) BOOL enabled;
@property (nonatomic, assign) BOOL hasLoaded;
@end

@implementation IAPGuardConfig

+ (instancetype)sharedConfig {
    static IAPGuardConfig *config = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        config = [[IAPGuardConfig alloc] init];
        [config reloadIfNeeded];
    });
    return config;
}

- (instancetype)init {
    self = [super init];
    if (self) {
        _enabled = YES;
        _allowedPrices = [NSSet set];
        _allowedPriceQuotas = @{};
        _hasAllowedPriceQuotas = NO;
        _resolvedConfigPath = [self resolveConfigPath];
    }
    return self;
}

- (void)reloadIfNeeded {
    // Purchase decisions call this path frequently; only parse the plist when mtime changes.
    NSDate *modificationDate = [self currentConfigModificationDate];

    @synchronized (self) {
        BOOL sameMissingFile = self.hasLoaded && modificationDate == nil && self.lastModificationDate == nil;
        BOOL sameExistingFile = self.hasLoaded && modificationDate != nil && [modificationDate isEqualToDate:self.lastModificationDate];
        if (sameMissingFile || sameExistingFile) {
            return;
        }
    }

    [self reloadWithModificationDate:modificationDate];
}

- (BOOL)isPriceAllowed:(NSString *)priceString {
    [self reloadIfNeeded];

    @synchronized (self) {
        return [self isPriceAllowedLocked:priceString];
    }
}

- (BOOL)consumeAllowanceForPrice:(NSString *)priceString remainingQuotaAfterConsume:(NSInteger *)remainingQuotaAfterConsume usedQuota:(BOOL *)usedQuota {
    // Quota is decremented when the purchase is allowed into StoreKit, not when Apple later completes it.
    [self reloadIfNeeded];

    @synchronized (self) {
        if (usedQuota) {
            *usedQuota = NO;
        }
        if (remainingQuotaAfterConsume) {
            *remainingQuotaAfterConsume = -1;
        }

        if (!self.enabled) {
            return YES;
        }

        NSString *trimmed = [self.class normalizedPriceString:priceString];
        if (trimmed.length == 0) {
            return NO;
        }

        if (!self.hasAllowedPriceQuotas) {
            return [self.allowedPrices containsObject:trimmed];
        }

        NSInteger remaining = [self.allowedPriceQuotas[trimmed] integerValue];
        if (remaining <= 0) {
            if (remainingQuotaAfterConsume) {
                *remainingQuotaAfterConsume = 0;
            }
            return NO;
        }

        NSInteger newRemaining = remaining - 1;
        NSMutableDictionary<NSString *, NSNumber *> *updatedQuotas = [self.allowedPriceQuotas mutableCopy];
        updatedQuotas[trimmed] = @(newRemaining);
        self.allowedPriceQuotas = [updatedQuotas copy];

        if (usedQuota) {
            *usedQuota = YES;
        }
        if (remainingQuotaAfterConsume) {
            *remainingQuotaAfterConsume = newRemaining;
        }

        [self writeAllowedPriceQuotasLocked:self.allowedPriceQuotas];
        return YES;
    }
}

- (NSSet<NSString *> *)allowedPricesSnapshot {
    [self reloadIfNeeded];

    @synchronized (self) {
        if (self.hasAllowedPriceQuotas) {
            return [NSSet setWithArray:self.allowedPriceQuotas.allKeys];
        }
        return [self.allowedPrices copy];
    }
}

- (NSString *)allowedPricesDisplayString {
    [self reloadIfNeeded];

    @synchronized (self) {
        if (self.hasAllowedPriceQuotas) {
            NSArray<NSString *> *keys = [self.allowedPriceQuotas.allKeys sortedArrayUsingSelector:@selector(compare:)];
            NSMutableArray<NSString *> *parts = [NSMutableArray array];
            for (NSString *price in keys) {
                [parts addObject:[NSString stringWithFormat:@"%@(%@)", price, self.allowedPriceQuotas[price]]];
            }
            return parts.count > 0 ? [parts componentsJoinedByString:@", "] : @"<空>";
        }

        NSArray<NSString *> *prices = [self.allowedPrices.allObjects sortedArrayUsingSelector:@selector(compare:)];
        return prices.count > 0 ? [prices componentsJoinedByString:@", "] : @"<空>";
    }
}

- (NSInteger)remainingQuotaForPrice:(NSString *)priceString {
    [self reloadIfNeeded];

    @synchronized (self) {
        if (!self.enabled || !self.hasAllowedPriceQuotas) {
            return kIAPGuardUnlimitedQuota;
        }

        NSString *trimmed = [self.class normalizedPriceString:priceString];
        if (trimmed.length == 0) {
            return 0;
        }
        return MAX((NSInteger)0, [self.allowedPriceQuotas[trimmed] integerValue]);
    }
}

- (NSString *)remainingQuotaDisplayStringForPrice:(NSString *)priceString {
    NSInteger remaining = [self remainingQuotaForPrice:priceString];
    if (remaining == kIAPGuardUnlimitedQuota) {
        return @"不限";
    }
    return [NSString stringWithFormat:@"%ld", (long)remaining];
}

- (NSString *)resolveConfigPath {
    NSString *resolved = jbroot(kIAPGuardRootFSConfigPath);
    if ([resolved isKindOfClass:[NSString class]] && resolved.length > 0) {
        return resolved;
    }
    return kIAPGuardRootFSConfigPath;
}

- (NSDate *)currentConfigModificationDate {
    NSString *path = self.resolvedConfigPath ?: [self resolveConfigPath];
    NSDictionary<NSFileAttributeKey, id> *attributes = [[NSFileManager defaultManager] attributesOfItemAtPath:path error:nil];
    NSDate *date = attributes[NSFileModificationDate];
    return [date isKindOfClass:[NSDate class]] ? date : nil;
}

- (void)reloadWithModificationDate:(NSDate *)modificationDate {
    NSString *path = self.resolvedConfigPath ?: [self resolveConfigPath];
    NSDictionary *dictionary = [NSDictionary dictionaryWithContentsOfFile:path];
    BOOL enabled = YES;
    NSSet<NSString *> *allowed = [NSSet set];
    NSDictionary<NSString *, NSNumber *> *quotas = @{};
    BOOL hasQuotas = NO;

    if ([dictionary isKindOfClass:[NSDictionary class]]) {
        id enabledValue = dictionary[@"enabled"];
        if ([enabledValue respondsToSelector:@selector(boolValue)]) {
            enabled = [enabledValue boolValue];
        }

        id pricesValue = dictionary[@"allowedPrices"];
        if ([pricesValue isKindOfClass:[NSArray class]]) {
            NSMutableSet<NSString *> *prices = [NSMutableSet set];
            for (id item in (NSArray *)pricesValue) {
                if (![item isKindOfClass:[NSString class]]) {
                    continue;
                }
                NSString *trimmed = [self.class normalizedPriceString:(NSString *)item];
                if (trimmed.length > 0) {
                    [prices addObject:trimmed];
                }
            }
            allowed = [prices copy];
        }

        id quotasValue = dictionary[@"allowedPriceQuotas"];
        if ([quotasValue isKindOfClass:[NSDictionary class]]) {
            hasQuotas = YES;
            NSMutableDictionary<NSString *, NSNumber *> *parsedQuotas = [NSMutableDictionary dictionary];
            [(NSDictionary *)quotasValue enumerateKeysAndObjectsUsingBlock:^(id key, id value, BOOL *stop) {
                if (![key isKindOfClass:[NSString class]] || ![value respondsToSelector:@selector(integerValue)]) {
                    return;
                }

                NSString *trimmed = [self.class normalizedPriceString:(NSString *)key];
                NSInteger quota = MAX((NSInteger)0, [value integerValue]);
                if (trimmed.length > 0) {
                    parsedQuotas[trimmed] = @(quota);
                }
            }];
            quotas = [parsedQuotas copy];
        }
    }

    @synchronized (self) {
        self.enabled = enabled;
        self.allowedPrices = allowed;
        self.allowedPriceQuotas = quotas;
        self.hasAllowedPriceQuotas = hasQuotas;
        self.lastModificationDate = modificationDate;
        self.hasLoaded = YES;
    }

}

- (BOOL)isPriceAllowedLocked:(NSString *)priceString {
    // Fail closed while enabled: missing/unknown prices are not allowed.
    if (!self.enabled) {
        return YES;
    }

    NSString *trimmed = [self.class normalizedPriceString:priceString];
    if (trimmed.length == 0) {
        return NO;
    }

    if (self.hasAllowedPriceQuotas) {
        return [self.allowedPriceQuotas[trimmed] integerValue] > 0;
    }

    return [self.allowedPrices containsObject:trimmed];
}

- (void)writeAllowedPriceQuotasLocked:(NSDictionary<NSString *, NSNumber *> *)quotas {
    NSString *path = self.resolvedConfigPath ?: [self resolveConfigPath];
    NSMutableDictionary *dictionary = [NSMutableDictionary dictionaryWithContentsOfFile:path];
    if (![dictionary isKindOfClass:[NSMutableDictionary class]]) {
        dictionary = [NSMutableDictionary dictionary];
    }

    dictionary[@"enabled"] = @(self.enabled);
    dictionary[@"allowedPriceQuotas"] = quotas ?: @{};
    [dictionary removeObjectForKey:@"allowedPrices"];

    [dictionary writeToFile:path atomically:YES];
    self.lastModificationDate = [self currentConfigModificationDate];
}

+ (NSString *)normalizedPriceString:(NSString *)priceString {
    if (![priceString isKindOfClass:[NSString class]]) {
        return @"";
    }
    return [priceString stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
}

@end
