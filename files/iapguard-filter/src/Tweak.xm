#import <Foundation/Foundation.h>
#import <StoreKit/StoreKit.h>
#import <UIKit/UIKit.h>
#import <objc/message.h>
#import <objc/runtime.h>
#import "IAPGuardConfig.h"
#import "IAPGuardFailedTransaction.h"
#import "IAPGuardDiagnostics.h"
#import "IAPGuardCancellationRelay.h"
static BOOL IAPGuardMRCompatibility = NO;
static char kIAPGuardCancellationFinished;
#if IAPGUARD_DIAGNOSTICS
#import <substrate.h>

static void IAPGuardTraceObserver(id observer) {
    Class cls = [observer class];
    SEL selector = @selector(paymentQueue:updatedTransactions:);
    if (!cls || ![observer respondsToSelector:selector]) return;
    static NSMutableSet *hooked;
    static dispatch_once_t once;
    dispatch_once(&once, ^{ hooked = [NSMutableSet set]; });
    @synchronized (hooked) {
        NSString *name = NSStringFromClass(cls);
        if ([hooked containsObject:name]) return;
        [hooked addObject:name];
        __block IMP original = NULL;
        IMP replacement = imp_implementationWithBlock(^(id target, SKPaymentQueue *queue, NSArray *transactions) {
            for (SKPaymentTransaction *transaction in transactions) {
                IAPGuardTrace(@"observer_enter class=%@ state=%ld error_code=%ld", name,
                              (long)transaction.transactionState, (long)transaction.error.code);
            }
            ((void (*)(id, SEL, SKPaymentQueue *, NSArray *))original)(target, selector, queue, transactions);
            IAPGuardTrace(@"observer_return class=%@", name);
        });
        MSHookMessageEx(cls, selector, replacement, &original);
    }
}
#endif

// Per-process StoreKit price cache. Each target app records its own product IDs at runtime.
static NSMutableDictionary<NSString *, NSString *> *IAPGuardProductPrices(void) {
    static NSMutableDictionary<NSString *, NSString *> *prices = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        prices = [NSMutableDictionary dictionary];
    });
    return prices;
}

static NSString *IAPGuardTrimmedString(NSString *value) {
    if (![value isKindOfClass:[NSString class]]) {
        return nil;
    }

    NSString *trimmed = [value stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
    return trimmed.length > 0 ? trimmed : nil;
}

// StoreKit 1 prices are captured from SKProduct responses instead of a hard-coded product ID list.
static void IAPGuardRecordProduct(SKProduct *product) {
    if (![product isKindOfClass:[SKProduct class]]) {
        return;
    }

    NSString *productIdentifier = nil;
    NSString *priceString = nil;
    @try {
        productIdentifier = IAPGuardTrimmedString(product.productIdentifier);
        priceString = IAPGuardTrimmedString(product.price.stringValue);
    } @catch (NSException *exception) {
    }

    if (!productIdentifier || !priceString) {
        return;
    }

    NSMutableDictionary<NSString *, NSString *> *prices = IAPGuardProductPrices();
    @synchronized (prices) {
        prices[productIdentifier] = priceString;
    }

}

static void IAPGuardRecordProducts(NSArray *products) {
    if (![products isKindOfClass:[NSArray class]]) {
        return;
    }

    for (id item in products) {
        IAPGuardRecordProduct((SKProduct *)item);
    }
}

static void IAPGuardRecordProductsResponse(SKProductsResponse *response) {
    if (![response isKindOfClass:[SKProductsResponse class]]) {
        return;
    }

    NSArray *products = nil;
    @try {
        products = response.products;
    } @catch (NSException *exception) {
    }

    IAPGuardRecordProducts(products);
}

static NSString *IAPGuardPriceForProductIdentifier(NSString *productIdentifier) {
    NSString *trimmed = IAPGuardTrimmedString(productIdentifier);
    if (!trimmed) {
        return nil;
    }

    NSMutableDictionary<NSString *, NSString *> *prices = IAPGuardProductPrices();
    @synchronized (prices) {
        return [prices[trimmed] copy];
    }
}

// Proxy preserves the app's original StoreKit delegate while recording the product response first.
@interface IAPGuardProductsRequestDelegateProxy : NSObject <SKProductsRequestDelegate>
@property (nonatomic, weak) id originalDelegate;
- (instancetype)initWithDelegate:(id)delegate;
@end

@implementation IAPGuardProductsRequestDelegateProxy

- (instancetype)initWithDelegate:(id)delegate {
    self = [super init];
    if (self) {
        _originalDelegate = delegate;
    }
    return self;
}

- (void)productsRequest:(SKProductsRequest *)request didReceiveResponse:(SKProductsResponse *)response {
    IAPGuardRecordProductsResponse(response);

    id delegate = self.originalDelegate;
    SEL selector = @selector(productsRequest:didReceiveResponse:);
    if ([delegate respondsToSelector:selector]) {
        void (*sendCallback)(id, SEL, SKProductsRequest *, SKProductsResponse *) = (void (*)(id, SEL, SKProductsRequest *, SKProductsResponse *))objc_msgSend;
        sendCallback(delegate, selector, request, response);
    }
}

- (BOOL)respondsToSelector:(SEL)aSelector {
    if (aSelector == @selector(productsRequest:didReceiveResponse:)) {
        return YES;
    }
    return [super respondsToSelector:aSelector] || [self.originalDelegate respondsToSelector:aSelector];
}

- (id)forwardingTargetForSelector:(SEL)aSelector {
    id delegate = self.originalDelegate;
    if ([delegate respondsToSelector:aSelector]) {
        return delegate;
    }
    return [super forwardingTargetForSelector:aSelector];
}

@end

static const void *kIAPGuardProductsRequestDelegateProxyKey = &kIAPGuardProductsRequestDelegateProxyKey;

static id IAPGuardProxyForProductsRequestDelegate(SKProductsRequest *request, id delegate) {
    if (!delegate) {
        objc_setAssociatedObject(request, kIAPGuardProductsRequestDelegateProxyKey, nil, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        return nil;
    }

    if ([delegate isKindOfClass:[IAPGuardProductsRequestDelegateProxy class]]) {
        return delegate;
    }

    IAPGuardProductsRequestDelegateProxy *proxy = [[IAPGuardProductsRequestDelegateProxy alloc] initWithDelegate:delegate];
    objc_setAssociatedObject(request, kIAPGuardProductsRequestDelegateProxyKey, proxy, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    return proxy;
}

static NSHashTable *IAPGuardObservers(void) {
    static NSHashTable *observers = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        observers = [NSHashTable weakObjectsHashTable];
    });
    return observers;
}

static NSMutableArray *IAPGuardDeniedTransactionArray(void) {
    static NSMutableArray *transactions = nil;
    static dispatch_once_t onceToken;
    dispatch_once(&onceToken, ^{
        transactions = [NSMutableArray array];
    });
    return transactions;
}

static NSArray *IAPGuardObserverSnapshot(void) {
    NSHashTable *observers = IAPGuardObservers();
    @synchronized (observers) {
        return observers.allObjects;
    }
}

static void IAPGuardAddObserver(id observer) {
    if (!observer) {
        return;
    }

    NSHashTable *observers = IAPGuardObservers();
    @synchronized (observers) {
        [observers addObject:observer];
    }
}

static void IAPGuardRemoveObserver(id observer) {
    if (!observer) {
        return;
    }

    NSHashTable *observers = IAPGuardObservers();
    @synchronized (observers) {
        [observers removeObject:observer];
    }
}

static void IAPGuardAddDeniedTransaction(id transaction) {
    if (!transaction) {
        return;
    }

    NSMutableArray *transactions = IAPGuardDeniedTransactionArray();
    @synchronized (transactions) {
        if (![transactions containsObject:transaction]) {
            [transactions addObject:transaction];
        }
    }
}

static BOOL IAPGuardIsDeniedTransaction(id transaction) {
    if (!transaction) {
        return NO;
    }

    NSMutableArray *transactions = IAPGuardDeniedTransactionArray();
    @synchronized (transactions) {
        return [transactions containsObject:transaction];
    }
}

static void IAPGuardRemoveDeniedTransaction(id transaction) {
    if (!transaction) {
        return;
    }

    NSMutableArray *transactions = IAPGuardDeniedTransactionArray();
    @synchronized (transactions) {
        [transactions removeObject:transaction];
    }
}

static NSArray *IAPGuardDeniedTransactionSnapshot(void) {
    NSMutableArray *transactions = IAPGuardDeniedTransactionArray();
    @synchronized (transactions) {
        return [transactions copy];
    }
}

static NSArray *IAPGuardMergedTransactions(NSArray *originalTransactions) {
    NSMutableArray *merged = [NSMutableArray array];

    if ([originalTransactions isKindOfClass:[NSArray class]]) {
        [merged addObjectsFromArray:originalTransactions];
    }

    NSArray *denied = IAPGuardDeniedTransactionSnapshot();
    for (id transaction in denied) {
        if (![merged containsObject:transaction]) {
            [merged addObject:transaction];
        }
    }

    return [merged copy];
}

static UIViewController *IAPGuardTopViewControllerFrom(UIViewController *viewController) {
    if (!viewController) {
        return nil;
    }

    UIViewController *presented = viewController.presentedViewController;
    if (presented) {
        return IAPGuardTopViewControllerFrom(presented);
    }

    if ([viewController isKindOfClass:[UINavigationController class]]) {
        return IAPGuardTopViewControllerFrom(((UINavigationController *)viewController).visibleViewController);
    }

    if ([viewController isKindOfClass:[UITabBarController class]]) {
        return IAPGuardTopViewControllerFrom(((UITabBarController *)viewController).selectedViewController);
    }

    return viewController;
}

static UIViewController *IAPGuardCurrentTopViewController(void) {
    UIWindow *keyWindow = nil;

    if (@available(iOS 13.0, *)) {
        NSSet<UIScene *> *connectedScenes = UIApplication.sharedApplication.connectedScenes;
        for (UIScene *scene in connectedScenes) {
            if (![scene isKindOfClass:[UIWindowScene class]]) {
                continue;
            }
            UIWindowScene *windowScene = (UIWindowScene *)scene;
            if (windowScene.activationState != UISceneActivationStateForegroundActive) {
                continue;
            }
            for (UIWindow *window in windowScene.windows) {
                if (window.isKeyWindow) {
                    keyWindow = window;
                    break;
                }
            }
            if (keyWindow) {
                break;
            }
        }
    }

    if (!keyWindow) {
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
        keyWindow = UIApplication.sharedApplication.keyWindow;
#pragma clang diagnostic pop
    }

    return IAPGuardTopViewControllerFrom(keyWindow.rootViewController);
}

static void IAPGuardPresentDeniedAlert(NSString *productIdentifier, NSString *priceString, NSString *remainingQuotaString) {
    NSString *product = productIdentifier.length > 0 ? productIdentifier : @"<未知商品>";
    NSString *price = priceString.length > 0 ? priceString : @"未知";
    NSString *allowedPrices = [[IAPGuardConfig sharedConfig] allowedPricesDisplayString];
    NSString *remainingQuota = remainingQuotaString.length > 0 ? remainingQuotaString : @"0";
    dispatch_async(dispatch_get_main_queue(), ^{
        UIViewController *topViewController = IAPGuardCurrentTopViewController();
        if (!topViewController) {
            IAPGuardTrace(@"alert_skipped=no_controller");
            return;
        }

        if ([topViewController isKindOfClass:[UIAlertController class]]) {
            IAPGuardTrace(@"alert_skipped=existing_alert");
            return;
        }

        NSString *message = [NSString stringWithFormat:@"商品：%@\n当前价格：%@\n允许价格：%@\n剩余次数：%@", product, price, allowedPrices, remainingQuota];
        UIAlertController *alert = [UIAlertController alertControllerWithTitle:@"购买已拦截"
                                                                       message:message
                                                                preferredStyle:UIAlertControllerStyleAlert];
        UIAlertAction *ok = [UIAlertAction actionWithTitle:@"确定" style:UIAlertActionStyleDefault handler:nil];
        [alert addAction:ok];
        IAPGuardTrace(@"alert_present_requested");
        [topViewController presentViewController:alert animated:YES completion:^{
            IAPGuardTrace(@"alert_present_completed");
        }];
    });
}

static void IAPGuardNotifyFailedPayment(SKPaymentQueue *queue, IAPGuardFailedTransaction *failedTransaction) {
    NSArray *observers = IAPGuardObserverSnapshot();
    IAPGuardTrace(@"denied_callback_scheduled observers=%lu", (unsigned long)observers.count);
    if (observers.count == 0) {
        return;
    }

    NSArray *updatedTransactions = @[failedTransaction];
    SEL callback = @selector(paymentQueue:updatedTransactions:);

    dispatch_async(dispatch_get_main_queue(), ^{
        for (id observer in observers) {
            if (![observer respondsToSelector:callback]) {
                continue;
            }

            @try {
                IAPGuardTrace(@"denied_callback_enter observer=%@", NSStringFromClass([observer class]));
                void (*sendCallback)(id, SEL, SKPaymentQueue *, NSArray *) = (void (*)(id, SEL, SKPaymentQueue *, NSArray *))objc_msgSend;
                sendCallback(observer, callback, queue, updatedTransactions);
                IAPGuardTrace(@"denied_callback_return");
            } @catch (NSException *exception) {
                IAPGuardTrace(@"denied_callback_exception name=%@", exception.name);
            }
        }
    });
}


%hook SKRequest

- (void)setDelegate:(id)delegate {
    Class productsRequestClass = NSClassFromString(@"SKProductsRequest");
    if (productsRequestClass && [self isKindOfClass:productsRequestClass]) {
        %orig(IAPGuardProxyForProductsRequestDelegate((SKProductsRequest *)self, delegate));
        return;
    }

    %orig(delegate);
}

- (id)delegate {
    id delegate = %orig;
    if ([delegate isKindOfClass:[IAPGuardProductsRequestDelegateProxy class]]) {
        return ((IAPGuardProductsRequestDelegateProxy *)delegate).originalDelegate;
    }
    return delegate;
}

%end

%hook SKProductsResponse

- (NSArray *)products {
    NSArray *products = %orig;
    IAPGuardRecordProducts(products);
    return products;
}

%end

%hook SKPaymentQueue

- (void)addTransactionObserver:(id)observer {
#if IAPGUARD_DIAGNOSTICS
    IAPGuardTraceObserver(observer);
#endif
    IAPGuardAddObserver(observer);
    %orig(observer);
}

- (void)removeTransactionObserver:(id)observer {
    IAPGuardRemoveObserver(observer);
    %orig(observer);
}

- (NSArray *)transactions {
    NSArray *originalTransactions = %orig;
    NSArray *mergedTransactions = IAPGuardMergedTransactions(originalTransactions);
    return mergedTransactions;
}

- (void)addPayment:(SKPayment *)payment {
    IAPGuardTrace(@"addPayment_enter main_thread=%d", [NSThread isMainThread]);
    // Gate purchase attempts by the recorded StoreKit price and the runtime allowedPrices plist.
    NSString *productIdentifier = nil;
    @try {
        productIdentifier = payment.productIdentifier;
    } @catch (NSException *exception) {
    }

    IAPGuardConfig *config = [IAPGuardConfig sharedConfig];
    [config reloadIfNeeded];

    NSString *priceString = IAPGuardPriceForProductIdentifier(productIdentifier);
    IAPGuardTrace(@"price_lookup price=%@", priceString ?: @"unknown");
    NSInteger remainingQuotaAfterConsume = -1;
    BOOL usedQuota = NO;
    if ([config consumeAllowanceForPrice:priceString remainingQuotaAfterConsume:&remainingQuotaAfterConsume usedQuota:&usedQuota]) {
        IAPGuardTrace(@"decision=allow quota_used=%d remaining=%ld", usedQuota, (long)remainingQuotaAfterConsume);
        %orig(payment);
        IAPGuardTrace(@"original_addPayment_return");
        return;
    }

    IAPGuardTrace(@"decision=deny");

    NSString *remainingQuotaString = [config remainingQuotaDisplayStringForPrice:priceString];
    IAPGuardFailedTransaction *failedTransaction = [[IAPGuardFailedTransaction alloc] initWithPayment:payment];
    IAPGuardAddDeniedTransaction(failedTransaction);
    IAPGuardNotifyFailedPayment(self, failedTransaction);
    IAPGuardPresentDeniedAlert(productIdentifier, priceString, remainingQuotaString);
}

- (void)finishTransaction:(SKPaymentTransaction *)transaction {
    IAPGuardTrace(@"finishTransaction state=%ld local_denial=%d error_code=%ld", (long)transaction.transactionState,
                  IAPGuardIsDeniedTransaction(transaction), (long)transaction.error.code);
    if ([transaction isKindOfClass:[IAPGuardFailedTransaction class]]) {
        IAPGuardRemoveDeniedTransaction(transaction);
        return;
    }

    // mr finishes a cancellation before the game's observer receives our relay.
    // A game observer may also finish it; send it to StoreKit only once.
    if (IAPGuardMRCompatibility && transaction.transactionState == SKPaymentTransactionStateFailed &&
        [transaction.error.domain isEqualToString:SKErrorDomain] && transaction.error.code == SKErrorPaymentCancelled) {
        @synchronized (transaction) {
            if (objc_getAssociatedObject(transaction, &kIAPGuardCancellationFinished)) return;
            objc_setAssociatedObject(transaction, &kIAPGuardCancellationFinished, @YES, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
        }
    }

    %orig(transaction);
}

%end

%group IAPGuardMRCancellation
%hook InsideAppStore
- (void)paymentQueue:(SKPaymentQueue *)queue updatedTransactions:(NSArray *)transactions {
    %orig(queue, transactions);
    // Keep mr's handling intact. Only restore the missing user-cancel event.
    NSArray *updates = [transactions copy];
    dispatch_async(dispatch_get_main_queue(), ^{
        IAPGuardRelayCancelledPayments(queue, updates, IAPGuardObserverSnapshot(), self);
    });
}
%end
%end

%ctor {
    %init;
    Class mrClass = NSClassFromString(@"InsideAppStore");
    const char *mrImage = mrClass ? class_getImageName(mrClass) : NULL;
    NSString *imageName = mrImage ? [[NSString stringWithUTF8String:mrImage] lastPathComponent] : nil;
    IAPGuardMRCompatibility = [NSBundle.mainBundle.bundleIdentifier isEqualToString:@"com.levelinfinite.sgameGlobal"] &&
        [imageName isEqualToString:@"mr.dylib"];
    if (IAPGuardMRCompatibility) {
        %init(IAPGuardMRCancellation);
    }
    IAPGuardTrace(@"loaded build=mr-cancel-compat-1 active=%d", IAPGuardMRCompatibility);
    [IAPGuardConfig sharedConfig];
    IAPGuardObservers();
    IAPGuardDeniedTransactionArray();
    IAPGuardProductPrices();
}
