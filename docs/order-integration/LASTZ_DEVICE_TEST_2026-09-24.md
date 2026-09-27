# Last Z 真机价格拦截测试（2026-09-24）

## 实际操作及结果

- 通过 XXTCloud 远控看到 Last Z 的“金砖不足提示”：购买 200 金砖，按钮价格为 US$1.99；不是 Apple 付款确认弹窗。
- XXTouch 检查确认前台 Bundle ID 为 `com.readygo.barrel.ios`。
- 在手机运行 `tests/lastz_price_probe.lua`，参数为 `LASTZ_TEST_ORDER={price='1.99',quantity=0}`。
- 实际运行时 plist 写入并回读一致：`enabled=true`、`allowedPriceQuotas={['1.99']=0}`。原配置已在手机上备份。
- 通过远控点击一次 US$1.99 入口。游戏显示加载转圈，随后回到原商品页，没有观察到“购买已拦截”或 Apple 付款确认弹窗。
- 点击后回读配置，仍为 `1.99 → 0`。未确认付款，未输入支付凭据。

## 结论

已验证真实手机上的远控操作、Lua 执行和运行时 plist 写入。**尚未验证购买拦截生效，不能据此认定价格限制通过。** 转圈结束且额度不变不足以证明拦截：请求可能尚未到达插件的 StoreKit 拦截点。

只读检查了手机的 `zzzIAPGuard.dylib`，包含 `allowedPriceQuotas`、额度消费方法、运行时 plist 路径和 UTF-16 的“购买已拦截”文案。文件具备这些特征仍不能证明它已注入 Last Z 或本次请求经过其 hook。

待定位：本次购买是否到达 StoreKit 1 `addPayment:`、IAPGuard 是否在 Last Z 进程生效，以及购买请求是否在更早阶段失败。未执行放行购买、扣次数或商品发货测试。

当前手机保留 `1.99 → 0 次` 配置，不自动恢复购买额度。该配置是插件全局配置，不是 Last Z 独立配置；本次验证直接使用设备脚本，未经过厦门订单接口。

## 后续定位

- 按 RootHide 官方源码确认路径后，只读检查 `RootHideConfig.plist`，Last Z 没有黑名单条目。来源：[RootHide 的黑名单读取代码](https://github.com/roothide/Dopamine2-roothide/blob/2.x/BaseBin/libjailbreak/src/roothider/blacklist.m)。
- 从运行进程环境确认存在 `DYLD_INSERT_LIBRARIES=/usr/lib/systemhook-2718193559BBE312.dylib`。这仅证明越狱基础注入，不证明 IAPGuard 已加载。
- 临时隔离环境安装 pymobiledevice3，通过已信任 USB 连接采集购买相关系统日志及 Last Z（进程名 `Survivor`）日志，并复现入口。采样中没有匹配到 IAPGuard、StoreKit、purchase、payment 的记录；日志缺失不能当作“没有调用”的证明。
- 未重复更改额度。仅重启 Last Z 一次，确认新进程成功启动。启动时出现带账号、密码、档位名称、价格、剩余数量的插件浮窗，未显示商品；关闭浮窗，没有填写凭据。
- 重启后游戏进入资源更新页面，曾停在 `5.48/6.33M`，后续完成更新并进入游戏场景。尚未返回商品页完成重启后的复测。
- 用户随后确认代充插件浮窗可直接关闭，不作为测试前置条件。仍需确认实际加载到 Last Z 的 IAPGuard 是否接到了 StoreKit 购买调用。没有修改网络、黑名单、插件二进制或账号数据。

诊断原始日志仅保存在本机 `/private/tmp/lastz-*-syslog.txt`，未作为仓库文件提交。当前结论仍是“真实配置写入通过，购买拦截未证实”，不是修复完成。

更新完成后再回读运行时 plist，仍为 `enabled=true`、`1.99 → 0`。加载期间捕获到应用暂停、网络请求取消等记录，但没有足够证据把这些记录认定为之前购买无响应的根因。

## 商品查询失败的直接证据

回到同一 US$1.99 商品入口后，于 16:29 再次采集 `Survivor` 日志并复现，得到：

```text
Pay_querySkuDetailsAsync: {"list":["lmios_54"],"type":"inapp"}
NativeIAP_RequestProduct_Start_lmios_54
NativeIAP_ProductsRequest_Success_ValidCount_0_InvalidCount_1
NativeIAP_InvalidProducts_lmios_54
```

已定位这次测试的前置阻塞：游戏查询 `lmios_54` 时得到无效商品，未获得有效商品信息。价格拦截源码的判断点在 `SKPaymentQueue addPayment:`，不能把商品查询失败当作价格拦截成功。为何商品无效（游戏版本、商店环境或其他插件影响等）尚未定因；不能仅凭无效商品日志断言是地区或网络问题。

对应原始日志保存在 `/private/tmp/lastz-purchase-after-restart.txt`，未提交仓库。

随后打开“其他金额”，点击 US$0.99（100 金砖）作对照。游戏没有发起新的商品查询，而是明确记录：

```text
[LUA][pay][CallPaymentByMoney] busy, ignore click pkgId=85011 waiting=false payState=1
```

因此不能将该次操作算作 0.99 商品有效性测试：上一次请求后游戏仍处于支付忙状态，后续点击被游戏忽略。原始对照日志为 `/private/tmp/lastz-099-compare.txt`。下一步需要查清无效商品的原因，已向用户确认游戏安装来源；未自行切换 Apple 账号、地区或重新安装游戏。
