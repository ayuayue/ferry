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
所以换壳不需要碰内核，也不需要签名驱动。

## 功能

- **连接**：每秒轮询正在走代理的 TCP/UDP 流（进程、主机、端口、命中规则、代理、上下行、年龄）。
- **右键一键建规则**：任意一行（或多选）→ 仅此主机 / 整进程 / `*.域名` / 让主机直连 / 阻断 /
  复制主机名 / 选中同进程。新规则插入到命中规则之前，完全相同的规则自动合并。
- **日志**：引擎回调的每一条新连接与引擎日志，支持搜索、只看 UDP/连接/会话，同样可右键建规则。
- **规则**：拖动排序（立即下发到引擎）、启用/禁用、编辑、删除，搜索过滤。
- **代理**：HTTP / SOCKS5 配置，流式测试输出（解析、TCP 连接、隧道、请求）。
- **选择正在运行的应用**：按名称/路径/PID 搜索，显示实例数与图标，不需要手输 exe 名。
- **服务安装**：设置页一键注册/卸载 `ProxyBridgeSvc`（弹 UAC），显示服务状态、引擎文件完整性、数据路径。

## 开发

```sh
npm install
npx mygo generate     # 生成 src/mygo.ts
npm run dev           # mygo dev：Go 与前端一起热更新
```

没有安装引擎服务时，界面会自动回落到**演示引擎**（进程内的假服务，合成连接与日志），
所以开发不需要管理员权限，也不需要 WebView2 之外的依赖。
演示引擎的管道名带进程号，dev 与已安装版本可以同时运行。

```sh
npm run build         # tsc + vite + go build + NSIS 安装包
```

产物在 `build/windows-amd64/`：`PBDeck.exe`、`engine/win/amd64/`（四个引擎文件）与
`pbdeck-setup-0.1.0-amd64.exe`。

## 目录

| 路径 | 用途 |
|---|---|
| `main.go` | mygo 窗口、绑定给前端的 API、引擎目录解析、提权安装入口 |
| `internal/pbproto/` | 协议 v6 编解码、命名管道客户端、演示引擎（mock） |
| `internal/session/` | 连接生命周期、规则重放、事件环形缓冲、流轮询、右键建规则 |
| `internal/model/` | 应用侧持久化（`%AppData%\PBDeck\settings.json`） |
| `internal/svc/` | 服务查询与安装/卸载（`sc` + `--setup-users`），UAC 提权 |
| `internal/procattr/` | 进程枚举、exe 图标转 PNG |
| `src/` | React 界面（连接 / 规则 / 代理 / 设置） |
| `resources/engine/win/amd64/` | 上游 4.3.0 二进制（MIT，见同目录 LICENSE） |
| `vendor/` | 无（依赖走 go.mod） |

## 限制

- 只支持 Windows：引擎依赖 WinDivert 与服务。
- 引擎只把**走代理**的流放进连接表；直连/阻断的连接只出现在日志里。
- UDP 仅在代理类型为 SOCKS5 时转发，HTTP 配置下的 UDP 会被引擎回退为直连。
- 域名规则依赖明文 DNS 嗅探，DoH/DoT 场景只按 IP 命中。
- 服务名固定为 `ProxyBridgeSvc`，与官方 ProxyBridge 安装包共存时会互相替换。
- 安装服务需要管理员；卸载同理。

## 许可

PBDeck 自身 MIT。引擎二进制来自 InterceptSuite/ProxyBridge（MIT），
`internal/pbproto` 的协议结构与编码按其公开头文件实现。
