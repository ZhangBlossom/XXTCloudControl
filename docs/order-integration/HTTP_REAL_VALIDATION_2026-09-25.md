# 本地真实 HTTP 验证（2026-09-25）

测试对象：正在运行的本地 Go 服务（127.0.0.1:46980）及真实手机当前订单；未使用模拟设备。
本轮仅请求订单、状态与可用量接口，不触发支付、远控操作、结束会话或清理。

订单：`honor-real-20260925-005`。执行时间：2026-09-25T18:57:31+08:00。

| 检查项 | 结果 | 证据 |
|---|---|---|
| 测试前会话 | 通过 | HTTP 200 |
| 使用中可用数量 | 通过 | available_count=0，estimated_wait_seconds=163 |
| 普通重复申请幂等 | 通过 | URL 与 expires_at 均保持相同 |
| 同订单变更参数 | 通过 | HTTP 409 / order_conflict |
| 非法数量 | 通过 | HTTP 400 / invalid_order |
| 4 个独立订单并发等待并取消 | 通过 | [(True, 15.0, 'client_timeout'), (True, 15.0, 'client_timeout'), (True, 15.0, 'client_timeout'), (True, 15.0, 'client_timeout')] |
| 等待取消未新增会话 | 通过 | 新增会话数=0 |
| 原会话保持有效 | 通过 | HTTP 200，截止时间未延长 |

等待请求由 Python HTTP 客户端在 15 秒主动取消，服务端未返回设备；取消后只读检查服务端订单安全记录，未出现这 4 个测试订单的会话。
本报告不记录远控链接或 token；价格拦截和手机页面操作由主控单独验收。

## 005自然到期后的验证

执行时间：2026-09-25T19:20:07+08:00。

| 检查项 | 结果 | 证据 |
|---|---|---|
| 过期旧token | 通过 | 401 / viewer_expired |
| 过期普通请求 | 通过 | 409 / not_active |
| 待清理可用数与预估 | 通过 | {"available_count": 0, "code": 200, "estimated_wait_seconds": null, "message": "成功", "next_available_at": null} |
| 非法currency=usd | 通过 | 400 / invalid_order |
| 非法country_code=USA | 通过 | 400 / invalid_order |
| 非法price=0 | 通过 | 400 / invalid_order |
| 非法price=1.001 | 通过 | 400 / invalid_order |
| retry等待其他设备 | 通过 | 等待5秒后客户端取消，未立即503或交付设备 |
| retry未重用待清理手机 | 通过 | 订单记录无新增，旧机仍pending_cleanup |

未调用释放或清理确认，未新增可用设备，未操控手机。
