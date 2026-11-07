# PBDeck

Windows 上的代理拦截器外壳：复用 [InterceptSuite/ProxyBridge](https://github.com/InterceptSuite/ProxyBridge)
4.3.0 的内核（WinDivert + `ProxyBridgeCore.dll`），用
[mygo](https://github.com/egoist/mygo)（Go + 系统 WebView）重写界面。

```
┌────────────────────────────┐
│ React 界面（本仓库 src/）    │
└─────────────┬──────────────┘
              │ mygo 绑定（同进程调用）
┌─────────────▼──────────────┐
│ Go 层 internal/session      │  规则/代理的真源，重放、事件缓冲、1s 轮询
└─────────────┬──────────────┘
              │ \\.\pipe\ProxyBridgeCtl / ProxyBridgeEvt（协议 v6）
┌─────────────▼──────────────┐
│ ProxyBridgeSvc（LocalSystem）│  未修改的上游服务，提权、持有驱动
└─────────────┬──────────────┘
              │
        ProxyBridgeCore.dll + WinDivert
```

之所以这么拆：上游 7.6k 行 C 引擎负责抓包/转发，服务与 GUI 之间本来就用命名管道，
所以换壳不需要碰内核，也不需要重新签名驱动。PBDeck 只做三件事：把规则重放进去、
把事件/连接表读出来、把交互做得顺手。

## 功能

- **连接**（按进程聚合）：每个应用的实例数、活动会话、请求数、上下行、最近活动；
  点行展开该进程的每条代理会话（目标、端口、命中规则、代理、流量、年龄）。
- **实时日志**：引擎回调的每一次新建连接（含直连与阻断），可按进程或域名归组，
  可按动作筛选、搜索、跟随；40px 虚拟化列表，几千条也不卡。
- **右键一键建规则**：连接、会话、日志、分组行都能右键 —— 仅此主机 / 整进程 /
  `*.域名` / 让主机直连 / 阻断主机 / 复制主机名。新规则插到命中规则之前，完全
  相同的规则自动合并；连接页支持多选批量生成。
- **规则**：拖动排序（立即下发）、多选批量删除、启用/禁用、编辑、搜索。
- **代理**：HTTP / SOCKS5 配置，流式测试输出（解析、TCP 连接、隧道、请求）。
- **选择正在运行的应用**：按名称/路径/PID 搜索，显示实例数、PID 与真实图标。
- **托盘与自启**：关闭窗口收进托盘，单击托盘恢复；设置页可开机自启（登录后静默启动）。
- **亮/暗主题**：默认亮色，跟随按钮切换，选择会持久化。
- **服务安装**：设置页一键注册/卸载 `ProxyBridgeSvc`（弹 UAC），显示服务状态、
  引擎文件完整性、数据路径；未安装时界面给出引导，而不是假数据。

## 开发

```sh
npm install
npx mygo generate     # 生成 src/mygo.ts
npm run dev           # mygo dev：Go 与前端一起热更新
```

没有安装引擎服务时，应用会停在「未连接」引导页并提供「安装引擎服务 / 重试连接」，
不会用演示数据冒充真实流量。要真正拦截流量必须安装服务（管理员 + WinDivert）。

```sh
npm run build         # tsc + vite + go build + NSIS 安装包
```

产物在 `build/windows-amd64/`：`PBDeck.exe`、`engine/win/amd64/`（四个引擎文件）与
`pbdeck-setup-0.1.0-amd64.exe`。

## 目录

| 路径 | 用途 |
|---|---|
| `main.go` | mygo 窗口、托盘、绑定给前端的 API、引擎目录解析、提权安装入口 |
| `internal/pbproto/` | 协议 v6 编解码、命名管道客户端（ctl 消息模式 + evt 字节流分帧） |
| `internal/session/` | 连接生命周期、规则重放、事件环形缓冲、流轮询、右键建规则 |
| `internal/model/` | 应用侧持久化（`%AppData%\PBDeck\settings.json`，含主题/页面） |
| `internal/svc/` | 服务查询与安装/卸载（`sc` + `--setup-users`），UAC 提权 |
| `internal/procattr/` | 进程枚举、exe 图标转 PNG |
| `src/` | React 界面（连接 / 实时日志 / 规则 / 代理 / 设置） |
| `resources/engine/win/amd64/` | 上游 4.3.0 二进制（MIT，见同目录 LICENSE） |
| `docs/protocol.md` | 管道协议与安装步骤备忘 |

## 限制

- 只支持 Windows：引擎依赖 WinDivert 与服务。
- 引擎只把**走代理**的流放进连接表；直连/阻断的连接只出现在日志里。
- 日志事件只带目标 IP；域名列在有对应会话时补全，否则显示 IP。
- UDP 仅在代理类型为 SOCKS5 时转发，HTTP 配置下的 UDP 会被引擎回退为直连。
- 域名规则依赖明文 DNS 嗅探，DoH/DoT 场景只按 IP 命中。
- 服务名固定为 `ProxyBridgeSvc`，与官方 ProxyBridge 安装包共存时会互相替换。
- 安装/卸载服务需要管理员。

## 许可
