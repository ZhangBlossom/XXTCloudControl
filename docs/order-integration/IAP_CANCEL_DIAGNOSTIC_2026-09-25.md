# 取消付款后转圈：真机诊断记录

## 复现与证据

测试设备：iPhone 12 Pro Max，iOS 16.0.2，RootHide；游戏 com.levelinfinite.sgameGlobal。
本轮未确认或完成付款。诊断包 1.0-diag1，源码日志默认关闭，构建时 IAPGUARD_DIAGNOSTICS=1 开启。

- 20:57:46，0.29 配额 0：addPayment 进入，decision=deny；三个观察者收到拒绝回调，手机成功显示“购买已拦截”。
- 20:59:50，0.29 配额 1：consume before=1；quota_write success=0 readback_matches=0；插件仍然 decision=allow remaining=0，App Store 显示 US$0.29 付款确认页。
- 21:00:29，点击取消，finishTransaction state=2 error_code=2。
- 取消后游戏持续转圈；再次点击商品没有产生第二条 addPayment_enter。

## 已确认与未确认

配额持久化失败但仍放行已确认。插件内存计数与手机 plist、服务端计数不一致；现有实现忽略写入结果。
转圈期间第二次请求未到达插件购买入口，因此不能称为“次数用完后插件不弹窗”。
取消回调卡在哪一层、以及写回失败的具体系统错误，仍需进一步诊断。

## 安装与回滚

原插件、filter、runtime 已备份在手机 XXTouch lua/scripts/iapguard-before-diag1-*。
原始 deb 仍保存在仓库 files/iapguard-filter/packages。
日志版包不带 runtime，安装后恢复了原 runtime 内容。
诊断时手机 filter 缩小为 com.levelinfinite.sgameGlobal，避免注入其他系统进程。
手机内 RootHide dpkg 访问 XXTouch 文件时需使用 /rootfs/var/mobile/Media/1ferver/...。

采集工具：tests/capture_iapguard_diagnostics.py，仅采集 [IAPGuardDiag] 行，不记录账号、收据或支付凭据。

## 修复后验证（21:55—21:57）

修复版 1.0-diag3 使用游戏自己的 Library/Preferences 配置。真实日志显示首次 0.29 请求 quota_write success=1、readback_matches=1，随后才 decision=allow remaining=0。付款确认页出现时，通过 USB 独立读取 plist 确认 0.29 剩余 0。取消付款后再次点击同价商品，decision=deny，真机显示购买已拦截；没有实际付款。截图：evidence/honor-cancel-attempt-exhausted.png。

同一版本还实际拒绝过 0.99 错价请求。原生回归测试验证写失败拒绝、精确价匹配、扣减落盘及重载后耗尽；旧代码写失败仍放行，红测失败，修复后通过。

此轮为隔离诊断，临时停止 mr.dylib 注入；取消后游戏恢复可操作。此前启用 mr 时，取消只进入 InsideAppStore，第二次点击未进入 addPayment。mr 是兼容性问题，未修改其二进制，也未实现支付结果回调补偿。新规则按进入付款流程的尝试次数扣减，不因取消或失败退次数。

诊断使用过十五分钟窗口以容纳远控审批耗时；正式订单仍六分钟。server/data/order-profiles.json 已迁移到新容器路径，旧档案已备份。

## 2026-09-26 共存根因与兼容修复范围

两插件同时启用的复测再次确认：扣次已原子写入并回读成功，取消后只有 InsideAppStore 收到 failed/error 2，用户现场确认持续转圈。

未找到 mr 源码。对当前安装二进制只读分析显示：addTransactionObserver hook（arm64 0x4d44）比较监听器类名，只有 InsideAppStore 分支才调用原注册函数，其他直接返回；InsideAppStore updatedTransactions 的 Failed 分支（0xc958起）只移除自身HUD并 finishTransaction，未向游戏监听器转发。与停用mr的对照测试及回调日志相符。

兼容代码只在 Honor + InsideAppStore 来自 mr.dylib 的条件下启用。保留mr原有处理，仅将真实 StoreKit 用户取消事件转发给此前登记的游戏监听器。排除来源监听器并对同一交易去重，重复finish不再送到StoreKit；不转发成功/恢复/其他失败事件，不恢复配额，不伪造支付成功。该实现尚待本轮真机验收，不能将编译通过视为修复完成。

兼容修复已构建为 1.0-diag4，SHA256 499c487d57c63e033c4086edbe30532cf4cc768ed967db2fbb451390c731e3af。新增原生取消转发测试已红绿验证。安装Lua提交返回code0（仅表示脚本开始）；两次安装日志回读因工具自动审批超时未执行，无法确认安装完成，未进行修复后的真机共存验收。不能据此声明转圈已修复。mr保持启用，未进行真实付款。

## 2026-09-26 修复后真机验收：通过

1. 安装日志确认 1.0-diag4 已安装；游戏进程日志 mr-cancel-compat-1 active=1，MR filter 回读仍为 UIKit/Foundation，未停用。
2. 15:00:52，0.29 配额由1扣0，quota_write success=1 readback_matches=1，随后allow。
3. 15:01:00，InsideAppStore收到真实取消 state=2/error=2；cancel_relay成功通知CTIIAPInteractor、APMAnalytics。
4. 15:01:09、15:01:13 后续同价请求进入addPayment并deny；15:01:17的9.99错价请求deny。
5. 远控画面确认游戏恢复充值页，无持续转圈；再次点击0.29看到“购买已拦截，剩余次数0”。USB独立回读游戏容器plist仍为0。

截图：evidence/honor-mr-coexist-cancel-fixed-20260926.png。日志：evidence/honor-mr-coexist-cancel-fixed-20260926.log。本轮没有确认或完成付款。验证范围是此台iPhone、当前Honor和mr版本下的取消兼容、次数耗尽及错价拒绝；不代表真实到账、其他游戏或其他版本已验收。
