# 单台 iPhone 非交易测试记录

日期：2026-09-24。设备：iPhone 12 Pro Max / iOS 16.0.2，XXTouch 1.3.8-20260916150916（trollstore）。本地 XXTCloud Go 服务。

## 已验证

| 测试 | 结果与边界 |
| --- | --- |
| 设备接入 | 开启远程操作并重启 XXTouch 服务后，云控显示 1 台在线 |
| WebRTC 远控 | 实际画面约 20 FPS；点击输入框后手机弹出键盘 |
| 云控上传和执行 Lua | 管理台上传并启动 device_smoke 脚本，手机报告时间更新，三个 plist 检查均通过 |
| 真机 plist.write/read | 独立测试文件中 648×5、1.98×2、旧键替换、配额置零通过；没有改动正式配额 |
| 插件目录写权限 | 在 IAPGuard 真实配置目录写入独立 xxt-smoke-20260924.plist 并回读成功，最终置零 |
| App 启停 | 计算器 app.run 返回 0，app.is_running 为真；退出后为假；不代表目标游戏已验证 |
| 原生计时 | 5 秒短计时结果 5 秒；没有完成正式 5/6 分钟订单生命周期测试 |
| 脚本停止远控 | 延迟 5 秒后调用 /api/webrtc/stop 返回 HTTP 200；网页实际变为未连接；随后恢复远控 |
| IAPGuard 配额源码 | 在 macOS 编译运行原始 IAPGuardConfig.mm，仅替换 roothide 路径解析到临时文件：648 放行 5 次、第 6 次拒绝，328/998 拒绝；1.98 放行 2 次后拒绝、1.97/1.99 拒绝；扣次落盘及同一实例重新加载通过 |
| Go 回归 | go test -race -count=1 ./... 通过。首次沙箱内执行因不允许监听本地端口失败，获得执行权限后完整重跑通过 |

macOS 配额测试命令：`python3 tests/iapguard_config_smoke.py`。这运行真实配置类，不运行 iOS StoreKit Hook，不证明手机已安装二进制与仓库源码完全一致。

设备自检源码：`tests/device_smoke.lua`。只写独立测试 plist 和报告，读取插件路径与注入过滤配置，不创建订单、不充值、不清理账号。测试脚本已留在云控服务器及手机，可重复运行。

## 发现与待验证

1. 手机存在 zzzIAPGuard.dylib、注入过滤 plist 和运行时 plist。文件存在不代表目标游戏进程已成功注入。
2. 此 XXTouch 的 jbroot('/') 返回 '/'，IAPGuard 文件却位于 roothide 根目录。设备档案必须使用核实的真实 policy_path，不可直接沿用 XXTouch 的 jbroot 结果。该路径为当前手机当前环境特有，重新越狱后需复核。
3. 实测发现注入过滤包含 com.apple.StoreKit、com.apple.Foundation、com.apple.UIKit。未修改过滤文件，也未证明已限制到指定游戏。
4. 尚未指定目标游戏；未测试真实商品 648×5 / 第六次拒绝、低高价拒绝、StoreKit 路径、币种与 quantity 边界、取消交易后的次数策略、到账或发货。
5. 游戏登录清理模块尚未提供或验收，没有运行删除账号数据操作，也没有将设备档案标记 verified=true。
6. 订单业务功能保持关闭。本次只验证基础能力，没有声称厦门端到端订单接口已可交付。最新外部文档的无鉴权、等待分配、可用设备查询、地区字段、6 分钟，与此前实现仍有差异；需单独对齐代码后验收。
7. 当前仅 1 台设备，未验证 5 台并发、断网恢复或长期运行。

## 手机生成的结果

以下为实际手机回读结果，不包含登录密码或游戏账号。

### xxt-device-smoke-cloud-20260924

```json
{
  "kind": "non_purchase_device_smoke",
  "completed_at": 1790232242,
  "inventory": [
    {
      "path": "/var/containers/Bundle/Application/.jbroot-2718193559BBE312/Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.dylib",
      "exists": true
    },
    {
      "filter": {
        "Bundles": [
          "com.apple.StoreKit",
          "com.apple.Foundation",
          "com.apple.UIKit"
        ]
      },
      "plist_readable": true,
      "path": "/var/containers/Bundle/Application/.jbroot-2718193559BBE312/Library/MobileSubstrate/DynamicLibraries/zzzIAPGuard.plist",
      "exists": true
    },
    {
      "plist_readable": true,
      "path": "/var/containers/Bundle/Application/.jbroot-2718193559BBE312/var/mobile/Library/Preferences/com.iapguard.runtime.plist",
      "exists": true
    },
    {
      "path": "/var/containers/Bundle/Application/.jbroot-2718193559BBE312/var/lib/dpkg/status",
      "exists": true
    }
  ],
  "checks": {
    "plist_integer_quota": {
      "result": {
        "enabled": true,
        "allowedPriceQuotas": {
          "648": 5
        }
      },
      "ok": true
    },
    "plist_zero_quota": {
      "result": {
        "enabled": true,
        "allowedPriceQuotas": {
          "1.98": 0
        }
      },
      "ok": true
    },
    "plist_decimal_and_replace": {
      "result": {
        "enabled": true,
        "allowedPriceQuotas": {
          "1.98": 2
        }
      },
      "ok": true
    }
  },
  "jbroot": "/"
}
```

### xxt-native-contract-20260924

```json
{
  "globals": {
    "json": "table",
    "plist": "table",
    "app": "table"
  },
  "checks": {
    "app_stop": {
      "ok": true,
      "result": true
    },
    "plugin_directory_write_read": {
      "ok": true,
      "result": "isolated sibling plist readable and writable; production policy unchanged"
    },
    "native_timer": {
      "ok": true,
      "result": {
        "requested_seconds": 5,
        "elapsed_seconds": 5
      }
    },
    "app_start": {
      "ok": true,
      "result": {
        "running": true,
        "return_code": 0
      }
    }
  },
  "completed_at": 1790232392
}
```

### xxt-stop-media-20260924

```json
{
  "stopped_at": 1790232534,
  "http_status": 200
}
```
