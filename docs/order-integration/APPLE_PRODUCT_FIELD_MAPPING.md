# 苹果商品字段与厦门订单字段对照（待实现）

本文是字段设计及实测记录，不表示正式接口已支持商品 ID 和币种联合校验。

## 当前真机证据

XXTCloud 已恢复远控。当前应用为 `com.levelinfinite.sgameGlobal`，不是 Last Z。手机 IAPGuard 拦截弹窗显示：

```json
{
  "bundle_id": "com.levelinfinite.sgameGlobal",
  "product_id": "com.levelinfinite.sgameGlobal_23_ie",
  "price": 0.29
}
```

以上是从前台应用信息与拦截弹窗整理的记录，不是苹果 HTTP 响应原文。弹窗显示当前允许价格 `1.99(0)`、剩余次数 `0`。

游戏页面显示 `USD$0.29`。币种 USD 有页面证据，但本轮没有从 SKProduct 对象导出 currencyCode；商品的 localizedTitle 也尚未导出，不能将页面礼包名称直接当作苹果商品标题。短时采集当前游戏日志未发现完整商品对象。再次进入购买出现“已有未支付订单”提示，已取消，未继续支付。

## 字段对应

| 厦门请求字段 | 来源或含义 | 建议用途 |
|---|---|---|
| order_id | 厦门业务订单号，并非苹果交易号 | 必填，关联请求和重试 |
| bundle_id | iOS 应用 Bundle ID，不是 SKProduct 字段 | 必填，限定游戏 |
| product_id | SKProduct.productIdentifier；购买时 SKPayment.productIdentifier | 必填，限定苹果内购商品 |
| price | SKProduct.price，十进制金额 | 必填，精确匹配；不是厦门折后收款金额 |
| currency | SKProduct.priceLocale.currencyCode | 必填，使用 USD/CNY/HKD 等代码，不使用美元符号，不从手机系统地区推断 |
| product_name | SKProduct.localizedTitle 或双方约定的展示名称 | 展示与核对，不作为唯一匹配条件 |
| quantity | 厦门允许放行的购买请求次数 | 必填；不是自动照抄 SKPayment.quantity，后者是单笔购买件数 |
| country_code | 业务国家或地区信息 | 可保留；不等于币种，也不自动修改苹果商店地区 |

插件应验证应用、商品 ID、价格、币种和额度；关键商品信息缺失时拒绝，不退回仅按价格放行。商品名称可能随商店语言变化，不能代替 product_id。同一内购商品 ID 若被游戏复用于不同礼包，仅靠 StoreKit 无法区分全部游戏内发货内容。

## 后续采集与改造

在现有 `IAPGuardRecordProduct(SKProduct *product)` 中增加白名单字段采集：productIdentifier、price 的十进制字符串、priceLocale.currencyCode、localizedTitle。仅输出这些商品字段和当前 Bundle ID，不输出账号、收据或支付凭据。这样可以获得真正来自 StoreKit 商品对象的结构化样本。

当前插件只保存“商品 ID → 价格”，runtime plist 只限制价格及次数。要实现上述联合校验，需要同时更新插件、运行时配置、Go 请求结构和 Lua 下发参数。正式文档目前仍限定 CNY；上述 currency 必填和 product_id 必填属于拟调整项。

参考：[Apple SKProduct](https://developer.apple.com/documentation/storekit/skproduct?language=objc)、[NSLocale.currencyCode](https://developer.apple.com/documentation/foundation/nslocale/currencycode?language=objc)、[SKPayment.productIdentifier](https://developer.apple.com/documentation/storekit/skpayment/productidentifier?language=objc)。
