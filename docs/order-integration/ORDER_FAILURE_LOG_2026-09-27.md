# 订单失败记录与本轮验证

## 本地文件

事件追加写入服务数据目录的 `order-events.jsonl`，每行一个 JSON，文件权限 0600。
现有 `order-sessions.json` 继续记录订单当前状态；事件日志保留历史，不因后续回收覆盖之前失败原因。

记录字段：`time`（服务器 UTC 时间）、`order_id`、`session_id`、`device_id`、`event`、`state`、`reason`。
不记录远控 token、完整链接、账号密码、支付凭据、SDP 或浏览器任意错误文本。

| event / reason | 含义 |
|---|---|
| viewer_failure / connection_failed | 浏览器报告远控连接失败 |
| viewer_failure / signal_failed | 浏览器报告信令或状态请求失败 |
| viewer_failure / remote_disconnected | 浏览器收到远端断开通知 |
| viewer_failure / browser_offline | 浏览器收到离线事件；只有上报能送达时才写入 |
| viewer_failure / device_signal_failed | 服务端调用设备远控信令失败 |
| viewer_failure / viewer_heartbeat_timeout | 曾打开的页面超过 30 秒未请求状态；可能是网络故障、关页或后台节流，不能据此确定网络故障 |
| session_state | 订单状态/原因变化，例如 expired、released、cleanup_failed；不是全部都代表故障 |

浏览器每 2 秒请求状态作为心跳；服务端订单循环检测失联。未打开过远控页面不算失联，正常到期不记心跳故障。
同一会话、同一客户端失败原因去重；每个连续失联时段只记录一次心跳超时。
失败上报不补次数、不延长会话、不自动释放设备，也不自动重连。
文件写失败时，上报接口返回错误；后台写失败会记录服务器错误日志，不伪称已落盘。

页面内部新增受订单 token 保护的 `/order-viewer/v1/:id/failure`，仅接受固定原因码。这不是给厦门新增的业务接口。
厦门仍只使用申请手机和查询设备两个接口，申请等待方式未变，也未新增严格 FIFO 队列。

## 验证范围

- Go 测试：失败事件鉴权、非法/附加字段拒绝、20 并发重复上报仅一条、日志权限及不泄漏 token、写失败可重试。
- Go 测试：未打开页面不报错、30 秒心跳失联、持续失联不刷屏、正常到期单独记录。
- 实际 viewer JavaScript 在模拟 fetch/WebRTC 环境执行：离线/连接/信令失败上报，401、到期、离开页面不误报客户端网络失败。
- 两台模拟设备：A/B 独占分配，C 等待，待清理不可分配，人工确认后 C 取得 A 原设备，旧 token 失效。
- 12 个并发申请：两台模拟设备只分配两单，其余等待直到客户端取消。

以上不等同于两台真实手机测试；本轮没有操作手机或验证真实断网。用户数据定向清理仍未验收，不能按固定时间将清理失败设备变为空闲。

## 本轮结果与本地运行

- `go test -race ./...` 通过（8.562 秒），构建通过。
- `node tests/order_viewer_events.mjs` 通过。
- 本地已加载 `/private/tmp/xxtcloud-order-events-server`；后续会话使用 manual_test 模式，到期断开并进入待清理，需人工确认才释放。
- 原有清理失败设备继续隔离，未更改为 completed、未重新出租；重启前的订单记录和配置均有本地备份。
- 日志实际位置：`server/data/order-events.jsonl`。本轮没有伪造真实订单失败，文件在发生后续事件时追加记录。
- 当前仍是本机联调配置，不是厦门公网部署；没有新增自动重连、FIFO 或两分钟强制释放。
