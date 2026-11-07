# 引擎协议（v6）

PBDeck 不链接引擎，而是像上游 GUI 一样通过两条命名管道驱动 `ProxyBridgeSvc`。

| 管道 | 方向 | 模式 |
|---|---|---|
| `\\.\pipe\ProxyBridgeCtl` | 客户端 → 服务 → 客户端 | 双向，可设消息模式 |
| `\\.\pipe\ProxyBridgeEvt` | 服务 → 客户端 | 只出不进，**客户端只能按字节流读** |

`ProxyBridgeEvt` 由服务用 `PIPE_ACCESS_OUTBOUND` 创建，客户端以 `GENERIC_READ` 打开后
`SetNamedPipeHandleState(PIPE_READMODE_MESSAGE)` 会返回 `ERROR_ACCESS_DENIED`（上游 GUI 同样
忽略这个失败）。因此客户端对事件管道必须自行分帧：`pbproto.parseEventAt` 累积字节，
数据不够时返回 `errNeedMore`，超过 1MB 丢弃重同步。

## 帧

- 请求：`u32 op` + 参数；响应：`u32 ret`（0 = 成功）+ 数据，**除了 `GET_FLOWS`**，它的响应直接是
  `u32 count` + 行，没有 `ret`。
- 字符串：`u32 长度` + UTF-8 字节（无 NUL）；`0xFFFFFFFF` 表示 nil。
- 上限：消息 64KB，字符串 32KB。

## 操作

`HELLO`(1) · `ADD_PROXY`(2) · `EDIT_PROXY`(3) · `DELETE_PROXY`(4) · `TEST_PROXY_START`(5) ·
`ADD_RULE`(6) · `ENABLE_RULE`(7) · `DISABLE_RULE`(8) · `DELETE_RULE`(9) · `EDIT_RULE`(10) ·
`MOVE_RULE`(11) · `GET_RULE_POS`(12) · `SET_TRAFFIC_LOG`(13) · `CLEAR_CONN_LOGS`(14) ·
`START`(15) · `STOP`(16) · `SET_ENABLED`(17) · `SET_LOG_ALL`(18) · `GET_FLOWS`(19)

`ADD_PROXY` / `ADD_RULE` 的 `ret` 就是新对象的 id（服务内部递增，重连后重新分配）。

## 事件

`PBEVT_LOG`(1) 文本 · `PBEVT_CONN`(2) 进程/PID/IP/端口/描述 · `PBEVT_TEST_LINE`(3) 文本 ·
`PBEVT_TEST_DONE`(4) 结果。

**注意**：`PBEVT_CONN` 只带 IP，不带域名；域名只有 `GET_FLOWS` 的行里才有。
所以“按域名建规则”只能从连接表（或已经带上域名的会话行）出发。

## 服务生命周期

服务是**无状态**的：每接受一个客户端就 `PB_LoadDirect` 起一份全新引擎，客户端断开即
`Stop()` 并卸载。规则与代理不存在服务里，只能由客户端在连接后按顺序重放。
这也是 PBDeck 把 `settings.json` 当唯一真源、每次连接全量重放的原因。

服务名在 `StartServiceCtrlDispatcherW` 中硬编码为 `ProxyBridgeSvc`，无法改名；
安装步骤与上游 NSIS 一致：`sc create` → `sc description` → `sc sdset`（放行交互用户）→
`ProxyBridgeSvc.exe --setup-users` → `sc start`。
