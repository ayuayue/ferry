# Ferry 在 Linux 上

Linux 版复用同一套界面与规则模型，但引擎与控制通道是另一套实现：

```
React 界面（本仓库 src/）
      │ mygo 绑定（同进程调用）
Go 层 internal/session      规则/代理的真源，重放、事件缓冲、1s 轮询
      │ /run/proxybridge/ctl.sock（字段帧，IPC 协议 v1）
 ferry-engine.service       上游 ProxyBridge 4.0.0 Linux，eBPF 数据面
```

Windows 用命名管道 + WinDivert，Linux 用 Unix 套接字 + eBPF，两者协议不同，
所以 `internal/pbproto`、`internal/svc`、`internal/procattr` 都按平台分成两份实现，
上层 `internal/session` 与前端一行不用改。

## 安装

```sh
sudo apt install ./ferry_1.1.0_amd64.deb      # 需要 libgtk-3-0、libwebkit2gtk-4.1-0
ferry                                          # 普通用户运行，不需要 root
```

首次启动后在「设置 → 引擎」点「安装引擎服务」，会弹出系统授权框（pkexec）；
没有 pkexec 的桌面环境里，在终端执行一次：

```sh
sudo ferry --install-engine
```

安装做的事：

| 项目 | 位置 |
|---|---|
| 应用 | `/opt/ferry/`（二进制与 `engine/`、`licenses/` 同级） |
| 内核 | `/usr/lib/ferry/engine/ProxyBridge` |
| systemd 单元 | `/etc/systemd/system/ferry-engine.service` |
| 控制套接字 | `/run/proxybridge/ctl.sock`（由 `RuntimeDirectory` 创建） |
| 引擎配置 | `/etc/proxybridge`（0700） |
| 用户组 | `proxybridge`，安装者会被自动加入 |

加入用户组立即生效（引擎每次都从 `/etc/group` 现读成员），不需要重新登录。
可以用 `id -nG | grep proxybridge` 确认。

卸载用界面上的「卸载」，或 `sudo ferry --uninstall-engine`，或 `dpkg -r ferry`；
三者都会 `disable --now` 并删除 Ferry 自己的 `ferry-engine.service`，不会动官方的
`proxybridge.service`。

## 为什么不直接用上游的 `--print-unit`

上游生成的单元带 `ProtectSystem=strict`、`ProtectKernelTunables=yes`、
`PrivateDevices=yes`、`RestrictNamespaces=yes`、`MemoryDenyWriteExecute=yes`。
实测这样服务能起来，日志也打印 `eBPF datapath`，但**一条连接都不会被拦截**：
`journalctl` 里没有任何 `[CONN]`，流量全走直连。Ferry 改用自己维护的单元文本
（保留能力集与 `ReadWritePaths`，去掉会掐断 eBPF 装载的沙箱项），装好后流量才有记录。

单元在 `internal/svc/svc_linux.go` 的 `unitTemplate` 里，改完重新安装即可生效。

## 与官方 ProxyBridge 的关系

两者抢的是同一个控制套接字。因此 Ferry 在安装和启动前都会检查：官方
`proxybridge.service` 正在运行、或者有 `/usr/local` 等 Ferry 引擎目录之外的
`ProxyBridge` 进程在跑时，拒绝启动并提示先停掉官方实例，不会去杀掉或替换它。
官方单元处于 `enabled` 时也会拒绝安装，需要先 `sudo systemctl disable proxybridge`。

## 平台差异

- 引擎的 `flows.get` **不回报每条会话命中了哪条规则**，所以连接页「规则」列在
  Linux 上显示 `—`，而不是猜一个。
- HTTP 代理不承载 UDP：这类规则的 UDP 部分会被引擎明确回退为直连，并写进日志。
  要 UDP 请用 SOCKS5。
- 存活时间极短、抓取时已消失的进程，引擎会以 `pid <n>` 占位；Ferry 会尝试用
  `/proc` 补回进程名，补不到就保留占位，不伪造名字。
- 进程图标取自 `/proc/<pid>/exe` 的可执行文件；拿不到时显示盾牌图标，没有图标的
  PNG 提取（Windows 用 `ExtractIconEx`）。
- 托盘图标依赖 `libayatana-appindicator3`，没有就直接不建托盘，主窗口照常工作。
- 界面字体跟随系统。精简系统里中文可能显示成方框，装 `fonts-noto-cjk` 即可；
  deb 已把它列为 `Recommends`。
- 规则里的进程名匹配是「文件名或完整路径」的通配符（`curl`、`fire*`、`/usr/bin/*`），
  大小写敏感。

## 排障

```sh
systemctl status ferry-engine.service      # 单元是否 active
journalctl -u ferry-engine -f              # 引擎日志：[CONN]、规则命中、错误
id -nG $USER | tr ' ' '\n' | grep proxybridge
ls -l /run/proxybridge/ctl.sock            # 套接字是否创建
```

- `未连接 / 套接字不存在`：引擎没起来。先看上面的 `status`，常见是没装单元，
  或 eBPF/BTF 缺失（内核需 ≥ 5.11、BTF、cgroup v2）。
- `权限不足`：当前用户不在 `proxybridge` 组，或组名被别的安装改过。
- 规则生效但连接表为空：确认「启用拦截」是开的，且引擎确实在跑；
  引擎只在 `engine.start` 之后才拦截。
