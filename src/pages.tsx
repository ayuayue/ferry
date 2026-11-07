import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  ArrowDownRight,
  ArrowUpRight,
  Ban,
  Cable,
  Check,
  ChevronDown,
  Copy,
  FolderOpen,
  GitBranch,
  Globe,
  GripVertical,
  Loader2,
  Network,
  Pencil,
  Play,
  PlugZap,
  Plus,
  Power,
  RefreshCw,
  Route,
  Search,
  Server,
  ShieldCheck,
  Trash2,
  X,
  Zap,
} from "lucide-react";
import clsx from "clsx";

import {
  ACTION_NAMES,
  PROTO_NAMES,
  PROXY_NAMES,
  api,
  describeRule,
  displayHost,
  eventAction,
  fmtAge,
  fmtBytes,
  fmtTime,
  isIP,
  matchChipClass,
  type AppStatus,
  type Flow,
  type LogEvent,
  type Proxy,
  type Rule,
  type RuleAction,
  type RuleTarget,
  type TestRun,
  type View,
} from "./bridge";
import { Confirm, RuleEditor, Toggle, type MenuItem, type MenuState } from "./components";

type Toast = (ok: boolean, title: string, detail?: string) => void;
type NewRule = (targets: RuleTarget[], mode?: "quick" | "custom") => void;

// ---------------------------------------------------------------- shared bits

const PROC_COLS = "30px minmax(180px, 1.4fr) 92px 92px 92px 92px 84px";
const FLOW_COLS = "30px minmax(200px, 1.8fr) 62px minmax(90px, .8fr) minmax(110px, 1fr) 78px 78px 62px";
const LOG_COLS = "84px 70px minmax(130px, .9fr) minmax(170px, 1.3fr) 58px minmax(120px, 1fr) 74px";

// iconCache holds data URLs by executable path; the process list is the
// name -> path map both pages share.
const iconCache = new Map<string, string>();
let procPaths: Map<string, string> | null = null;

async function loadProcPaths() {
  if (procPaths) return procPaths;
  const list = await api.processes();
  procPaths = new Map(list.map((p) => [p.name.toLowerCase(), p.path]));
  return procPaths;
}

function ProcIcon({ name }: { name: string }) {
  const [src, setSrc] = useState<string>("");
  useEffect(() => {
    let live = true;
    const key = name.toLowerCase();
    const cached = iconCache.get(key);
    if (cached) {
      setSrc(cached);
      return;
    }
    void (async () => {
      try {
        const paths = await loadProcPaths();
        const path = paths.get(key);
        if (!path) return;
        const icon = await api.icon(path);
        if (!live || !icon) return;
        iconCache.set(key, icon);
        setSrc(icon);
      } catch {
        /* the fallback letter is enough */
      }
    })();
    return () => {
      live = false;
    };
  }, [name]);
  if (!src) {
    return <span className="proc-ph">{(name || "?").slice(0, 1).toUpperCase()}</span>;
  }
  return <img className="proc-ico" src={src} alt="" />;
}

function Kpi({
  icon,
  label,
  value,
  foot,
  tone,
}: {
  icon: ReactNode;
  label: string;
  value: string;
  foot: string;
  tone?: "mint" | "coral";
}) {
  return (
    <div className={clsx("kpi", tone)}>
      <div className="kpi-top">
        <span className="kpi-ico">{icon}</span>
        {label}
      </div>
      <b>{value}</b>
      <span className="tip">{foot}</span>
    </div>
  );
}

// OfflineCard is the one thing both live pages show when the service is not
// there: a short explanation and the two ways forward.
function OfflineCard({
  status,
  onConnect,
  onSettings,
  what,
}: {
  status: AppStatus | null;
  onConnect: () => void;
  onSettings: () => void;
  what: string;
}) {
  const svc = status?.service;
  return (
    <div className="card setup-card">
      <div className="setup-ico">
        <PlugZap size={26} />
      </div>
      <div className="setup-main">
        <b>引擎服务未连接</b>
        <p>
          {what}由 ProxyBridge 服务产生；服务未安装或未启动时，这里不会有任何数据。
        </p>
        <div className="setup-state">
          <span className={clsx("chip", svc?.running ? "direct" : svc?.installed ? "proxy" : "muted")}>
            {svc?.running ? "服务运行中" : svc?.installed ? "已安装 · 未运行" : "未安装"}
          </span>
          {!status?.engineReady && <span className="chip block">引擎文件缺失</span>}
          {status?.admin && <span className="chip muted">管理员权限</span>}
        </div>
        <div className="row-actions">
          <button className="btn primary" onClick={onSettings}>
            <ShieldCheck size={14} /> {svc?.installed ? "打开设置检查服务" : "安装引擎服务"}
          </button>
          <button className="btn" onClick={onConnect}>
            <RefreshCw size={14} /> 重试连接
          </button>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------- connections

interface ProcGroup {
  key: string;
  name: string;
  pids: number[];
  flows: Flow[];
  events: LogEvent[];
  up: number;
  down: number;
  last: number;
}

export function ConnectionsPage({
  view,
  events,
  status,
  onConnect,
  onSettings,
  onMenu,
  onToast,
  onNewRule,
}: {
  view: View;
  events: LogEvent[];
  status: AppStatus | null;
  onConnect: () => void;
  onSettings: () => void;
  onMenu: (m: MenuState) => void;
  onToast: Toast;
  onNewRule: NewRule;
}) {
  const [q, setQ] = useState("");
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const groups = useMemo(() => groupByProcess(view.flows, events, q), [view.flows, events, q]);

  const toggle = (key: string) =>
    setExpanded((cur) => {
      const next = new Set(cur);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const targetFor = (f: Flow): RuleTarget => ({
    flowId: f.id,
    process: f.name,
    pid: f.pid,
    host: f.host,
    port: f.port,
    udp: f.udp,
    proxyKey: f.proxyKey,
    matchedRuleKey: f.ruleKey,
  });

  const procTarget = (g: ProcGroup): RuleTarget => ({
    process: g.name === "未知进程" ? "" : g.name,
    pid: g.pids[0] ?? 0,
    host: "",
    port: 0,
    udp: false,
    proxyKey: g.flows[0]?.proxyKey ?? "",
    matchedRuleKey: g.flows[0]?.ruleKey ?? "",
  });

  const act = async (
    targets: RuleTarget[],
    action: RuleAction,
    label: string,
    scope: "host" | "process" = "process",
  ) => {
    try {
      await api.rulesFromTargets(targets, {
        scope,
        action,
        proxyKey: "",
        ports: "*",
        localhost: false,
        moveToTop: false,
      });
      onToast(true, label, targets.map((t) => t.process || "该进程").join("、"));
    } catch (e) {
      onToast(false, "添加失败", String(e));
    }
  };

  const openProcMenu = (g: ProcGroup, x: number, y: number) => {
    const target = procTarget(g);
    const items: MenuItem[] = [
      {
        kind: "item",
        label: `为 ${g.name} 建规则：走代理`,
        hint: "整进程",
        icon: <GitBranch size={14} />,
        onSelect: () => onNewRule([target]),
      },
      {
        kind: "item",
        label: `让 ${g.name} 直连`,
        icon: <Route size={14} />,
        onSelect: () => void act([target], 1, "已添加直连规则"),
      },
      {
        kind: "item",
        label: `阻断 ${g.name}`,
        icon: <Ban size={14} />,
        danger: true,
        onSelect: () => void act([target], 2, "已添加阻断规则"),
      },
      { kind: "sep" },
      {
        kind: "item",
        label: "只看这个进程",
        icon: <Search size={14} />,
        onSelect: () => setQ(g.name),
      },
      {
        kind: "item",
        label: "复制进程名",
        icon: <Copy size={14} />,
        onSelect: () => {
          navigator.clipboard?.writeText(g.name).catch(() => undefined);
          onToast(true, "已复制", g.name);
        },
      },
    ];
    onMenu({ x, y, header: <span>{g.name}</span>, items });
  };

  const openFlowMenu = (f: Flow, x: number, y: number) => {
    const target = targetFor(f);
    const host = f.host || "此主机";
    const items: MenuItem[] = [
      {
        kind: "item",
        label: "建规则：仅此主机",
        hint: "此连接",
        icon: <Zap size={14} />,
        onSelect: () => onNewRule([target]),
      },
      {
        kind: "item",
        label: `建规则：${f.name || "该进程"} 的所有流量`,
        hint: "整进程",
        icon: <GitBranch size={14} />,
        onSelect: () => onNewRule([{ ...target, host: "", port: 0, flowId: 0 }]),
      },
    ];
    if (f.host && !isIP(f.host)) {
      items.push({
        kind: "item",
        label: `建规则：所有应用访问 ${rootDomain(f.host)}`,
        icon: <Globe size={14} />,
        onSelect: () =>
          onNewRule([{ ...target, process: "*", port: 0, flowId: 0, host: rootDomain(f.host) }]),
      });
    }
    items.push(
      { kind: "sep" },
      {
        kind: "item",
        label: `让 ${host} 直连`,
        icon: <Route size={14} />,
        onSelect: () => void act([{ ...target, process: "*" }], 1, "已添加直连规则", "host"),
      },
      {
        kind: "item",
        label: `阻断 ${host}`,
        icon: <Ban size={14} />,
        danger: true,
        onSelect: () => void act([{ ...target, process: "*" }], 2, "已添加阻断规则", "host"),
      },
      { kind: "sep" },
      {
        kind: "item",
        label: "复制主机名",
        icon: <Copy size={14} />,
        onSelect: () => {
          navigator.clipboard?.writeText(f.host ?? "").catch(() => undefined);
          onToast(true, "已复制", f.host);
        },
      },
    );
    onMenu({ x, y, header: <span>连接 #{f.id}</span>, items });
  };

  const selectedGroups = groups.filter((g) => picked.has(g.key));

  return (
    <>
      <div className="page-head">
        <div>
          <h1>连接</h1>
          <div className="sub">按进程聚合的活动会话 · 右键进程或连接可一键转成规则</div>
        </div>
        <div className="right">
          <div className="search" style={{ width: 230 }}>
            <Search />
            <input
              className="input"
              placeholder="筛选进程、主机、PID"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <div className="live-strip">
            <span className={view.connected ? "pulse" : "dot idle"} />
            {view.connected ? "实时" : "离线"}
          </div>
        </div>
      </div>

      <div className="page-body">
        {!view.connected ? (
          <OfflineCard
            status={status}
            onConnect={onConnect}
            onSettings={onSettings}
            what="连接会话"
          />
        ) : (
          <>
            <div className="kpis">
              <Kpi icon={<Network />} label="进程" value={String(groups.length)} foot="有流量的应用" />
              <Kpi
                icon={<Cable />}
                label="活动会话"
                value={String(view.flows.length)}
                foot={`累计 ${view.stats.flows}`}
              />
              <Kpi icon={<ArrowUpRight />} label="上行" value={fmtBytes(view.stats.up)} foot="本次会话" />
              <Kpi icon={<ArrowDownRight />} label="下行" value={fmtBytes(view.stats.down)} foot="本次会话" />
            </div>

            <div className="card table-card">
              {picked.size > 0 && (
                <div className="selbar">
                  <b>已选 {picked.size} 个进程</b>
                  <button
                    className="btn sm primary"
                    onClick={() => onNewRule(selectedGroups.map(procTarget))}
                  >
                    <Zap size={13} /> 生成整进程规则
                  </button>
                  <button className="btn sm ghost right" onClick={() => setPicked(new Set())}>
                    取消选择
                  </button>
                </div>
              )}

              <div className="table-head" style={{ gridTemplateColumns: PROC_COLS }}>
                <button
                  className={clsx("pick", picked.size > 0 && "on")}
                  title="全选进程"
                  onClick={() =>
                    setPicked(picked.size === groups.length ? new Set() : new Set(groups.map((g) => g.key)))
                  }
                />
                <span>进程</span>
                <span className="right">活动会话</span>
                <span className="right">请求</span>
                <span className="right">上行</span>
                <span className="right">下行</span>
                <span className="right">最近</span>
              </div>

              <div className="table-wrap">
                {groups.length === 0 && (
                  <div className="empty">
                    <Search />
                    <b>还没有匹配的进程</b>
                    <span>让应用产生一些走代理的流量，或换一个筛选条件。</span>
                  </div>
                )}
                {groups.map((g) => {
                  const open = expanded.has(g.key);
                  const sel = picked.has(g.key);
                  return (
                    <div key={g.key} className="group">
                      <div
                        className={clsx("row proc-row", open && "open", sel && "sel")}
                        style={{ gridTemplateColumns: PROC_COLS }}
                        onClick={() => toggle(g.key)}
                        onContextMenu={(e) => {
                          e.preventDefault();
                          openProcMenu(g, e.clientX, e.clientY);
                        }}
                      >
                        <button
                          className={clsx("pick", sel && "on")}
                          onClick={(e) => {
                            e.stopPropagation();
                            setPicked((cur) => {
                              const next = new Set(cur);
                              if (next.has(g.key)) next.delete(g.key);
                              else next.add(g.key);
                              return next;
                            });
                          }}
                        />
                        <div className="proc big">
                          <ProcIcon name={g.name} />
                          <div>
                            <div className="nm">
                              {g.name}
                              {g.pids.length > 1 && <span className="badge">{g.pids.length} 实例</span>}
                            </div>
                            <div className="pid">
                              {g.pids.slice(0, 3).map((p) => `PID ${p}`).join(" · ")}
                              {g.pids.length > 3 ? " …" : ""}
                            </div>
                          </div>
                        </div>
                        <div className="num right">{g.flows.length || "—"}</div>
                        <div className="num right dim">{g.events.length || "—"}</div>
                        <div className="num right">{fmtBytes(g.up)}</div>
                        <div className="num right">{fmtBytes(g.down)}</div>
                        <div className="num right dim">{g.last ? ago(g.last) : "—"}</div>
                      </div>

                      {open && (
                        <div className="subrows">
                          {g.flows.length === 0 && (
                            <div className="subempty">该进程当前没有走代理的会话；请求出现在实时日志里。</div>
                          )}
                          {g.flows.map((f) => (
                            <div
                              key={f.id}
                              className="row sub"
                              style={{ gridTemplateColumns: FLOW_COLS }}
                              onContextMenu={(e) => {
                                e.preventDefault();
                                openFlowMenu(f, e.clientX, e.clientY);
                              }}
                            >
                              <span className={clsx("dot", !f.udp && "tcp")} />
                              <div className="target mono" title={f.host}>
                                {f.host || "—"}
                                {f.ipv6 ? <span className="badge">v6</span> : null}
                                {f.udp ? <span className="badge">UDP</span> : null}
                              </div>
                              <div className="mono dim">{f.port}</div>
                              <div>
                                <span className="chip hit" title={f.ruleKey}>
                                  {f.ruleKey ? shortKey(f.ruleKey) : f.ruleId ? `#${f.ruleId}` : "默认"}
                                </span>
                              </div>
                              <div>
                                <span className={clsx("chip", f.proxyId ? "proxy" : "direct")}>
                                  {f.proxyName || (f.proxyId ? `#${f.proxyId}` : "直连")}
                                </span>
                              </div>
                              <div className="num right">{fmtBytes(f.sent)}</div>
                              <div className="num right">{fmtBytes(f.received)}</div>
                              <div className="num right dim">{fmtAge(f.ageMs)}</div>
                            </div>
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>

              <div className="table-foot">
                <span className="tip">会话来自引擎的实时流（仅走代理的）；请求数来自连接事件，含直连与阻断。</span>
                <span className="right tip">
                  <ChevronDown size={12} /> 点行展开 · 右键建规则
                </span>
              </div>
            </div>
          </>
        )}
      </div>
    </>
  );
}

function groupByProcess(flows: Flow[], events: LogEvent[], q: string): ProcGroup[] {
  const map = new Map<string, ProcGroup>();
  const get = (name: string) => {
    const key = name || "未知进程";
    let g = map.get(key);
    if (!g) {
      g = { key, name: key, pids: [], flows: [], events: [], up: 0, down: 0, last: 0 };
      map.set(key, g);
    }
    return g;
  };
  for (const f of flows) {
    const g = get(f.name);
    g.flows.push(f);
    if (f.pid && !g.pids.includes(f.pid)) g.pids.push(f.pid);
    g.up += f.sent;
    g.down += f.received;
    g.last = Math.max(g.last, Date.now() - f.ageMs);
  }
  for (const e of events) {
    if (e.kind !== "conn") continue;
    const g = get(e.process ?? "");
    g.events.push(e);
    if (e.pid && !g.pids.includes(e.pid)) g.pids.push(e.pid);
    const t = Date.parse(e.time);
    if (!Number.isNaN(t)) g.last = Math.max(g.last, t);
  }
  const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
  return Array.from(map.values())
    .filter((g) => {
      if (!terms.length) return true;
      const hay = `${g.name} ${g.pids.join(" ")} ${g.flows.map((f) => f.host).join(" ")}`.toLowerCase();
      return terms.every((t) => hay.includes(t));
    })
    .sort((a, b) => b.flows.length - a.flows.length || b.events.length - a.events.length);
}

function ago(ms: number) {
  const s = Math.max(0, Math.round((Date.now() - ms) / 1000));
  if (s < 5) return "刚刚";
  if (s < 60) return `${s}s 前`;
  if (s < 3600) return `${Math.floor(s / 60)}m 前`;
  return `${Math.floor(s / 3600)}h 前`;
}

// ---------------------------------------------------------------- live log

type LiveGroup = "none" | "process" | "host";

export function LiveLogPage({
  view,
  events,
  status,
  onConnect,
  onSettings,
  onMenu,
  onToast,
  onNewRule,
}: {
  view: View;
  events: LogEvent[];
  status: AppStatus | null;
  onConnect: () => void;
  onSettings: () => void;
  onMenu: (m: MenuState) => void;
  onToast: Toast;
  onNewRule: NewRule;
}) {
  const [q, setQ] = useState("");
  const [action, setAction] = useState<"all" | RuleAction>("all");
  const [group, setGroup] = useState<LiveGroup>("none");
  const [follow, setFollow] = useState(true);
  const scrollRef = useRef<HTMLDivElement>(null);

  const rows = useMemo(
    () => filterEvents(view, events, q, action),
    [view, events, q, action],
  );

  const vrows = useMemo(() => (group === "none" ? rows : []), [rows, group]);
  const virt = useVirtualizer({
    count: vrows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 40,
    overscan: 16,
  });

  // Following pins the newest row to the top. With following off a new event
  // still prepends a row, so shift the offset by the height the list grew and
  // the entries under the cursor stay put instead of jumping.
  const totalSize = virt.getTotalSize();
  const prevTotal = useRef(0);
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const grew = totalSize - prevTotal.current;
    prevTotal.current = totalSize;
    if (follow) el.scrollTop = 0;
    else if (grew > 0) el.scrollTop += grew;
  }, [totalSize, follow]);

  const grouped = useMemo(
    () => (group === "none" ? [] : groupEvents(rows, group, (e) => displayHost(e, view.flows))),
    [rows, group, view.flows],
  );

  const targetFor = (e: LogEvent): RuleTarget => ({
    process: e.process ?? "",
    pid: e.pid ?? 0,
    host: displayHost(e, view.flows),
    port: e.port ?? 0,
    udp: false,
    proxyKey: e.proxyKey,
    matchedRuleKey: e.ruleKey,
  });

  const openMenu = (e: LogEvent, x: number, y: number) => {
    const target = targetFor(e);
    const host = target.host;
    const items: MenuItem[] = [
      {
        kind: "item",
        label: `为 ${e.process || "该应用"} 建规则`,
        hint: "进程",
        icon: <GitBranch size={14} />,
        onSelect: () => onNewRule([{ ...target, host: "", port: 0 }]),
      },
      {
        kind: "item",
        label: host ? `为 ${host} 建规则` : "为此连接建规则",
        icon: <Zap size={14} />,
        disabled: !host,
        onSelect: () => onNewRule([target]),
      },
    ];
    if (host && !isIP(host)) {
      items.push({
        kind: "item",
        label: `所有应用访问 ${rootDomain(host)}`,
        icon: <Globe size={14} />,
        onSelect: () => onNewRule([{ ...target, process: "*", port: 0, host: rootDomain(host) }]),
      });
    }
    items.push(
      { kind: "sep" },
      {
        kind: "item",
        label: "复制主机",
        icon: <Copy size={14} />,
        disabled: !host,
        onSelect: () => {
          navigator.clipboard?.writeText(host).catch(() => undefined);
          onToast(true, "已复制", host);
        },
      },
      {
        kind: "item",
        label: "复制整行",
        icon: <Copy size={14} />,
        onSelect: () => {
          navigator.clipboard?.writeText(e.text || e.info || "").catch(() => undefined);
          onToast(true, "已复制");
        },
      },
    );
    onMenu({ x, y, header: <span>{e.kind === "conn" ? "连接事件" : "引擎消息"}</span>, items });
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>实时日志</h1>
          <div className="sub">引擎推送的每一次新建连接，含直连与阻断 · 可按进程或域名归组</div>
        </div>
        <div className="right">
          <div className="live-strip">
            <span className={view.connected ? "pulse" : "dot idle"} />
            {view.connected ? "实时" : "离线"}
          </div>
          <button className="btn" onClick={() => api.clearEvents()}>
            <Trash2 size={14} /> 清空
          </button>
        </div>
      </div>

      <div className="page-body">
        {!view.connected ? (
          <OfflineCard
            status={status}
            onConnect={onConnect}
            onSettings={onSettings}
            what="实时请求"
          />
        ) : (
          <div className="card table-card">
            <div className="toolbar">
              <div className="search grow">
                <Search />
                <input
                  className="input"
                  placeholder="筛选进程、域名、IP、端口…"
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                />
              </div>
              <div className="seg">
                {(
                  [
                    { k: "all", label: "全部" },
                    { k: 0, label: "代理" },
                    { k: 1, label: "直连" },
                    { k: 2, label: "阻断" },
                  ] as const
                ).map((t) => (
                  <button
                    key={String(t.k)}
                    className={clsx(action === t.k && "on")}
                    onClick={() => setAction(t.k)}
                  >
                    {t.label}
                  </button>
                ))}
              </div>
              <div className="seg">
                {(
                  [
                    { k: "none", label: "逐条" },
                    { k: "process", label: "按进程" },
                    { k: "host", label: "按域名" },
                  ] as const
                ).map((t) => (
                  <button
                    key={t.k}
                    className={clsx(group === t.k && "on")}
                    onClick={() => setGroup(t.k)}
                  >
                    {t.label}
                  </button>
                ))}
              </div>
              <label className="follow">
                <Toggle on={follow} onChange={setFollow} />
                跟随
              </label>
              <span className="num tip">{rows.length} 条</span>
            </div>

            <div className="table-head" style={{ gridTemplateColumns: group === "none" ? LOG_COLS : "74px 70px minmax(220px, 2fr) 90px 70px 1fr" }}>
              <span>{group === "none" ? "时间" : "最近"}</span>
              <span>动作</span>
              <span>{group === "none" ? "进程" : group === "process" ? "进程" : "域名"}</span>
              <span>{group === "none" ? "目标" : "累计"}</span>
              <span>{group === "none" ? "端口" : "会话"}</span>
              <span>{group === "none" ? "说明" : "最近目标"}</span>
              {group === "none" && <span className="right">规则</span>}
            </div>

            {group === "none" ? (
              <div className="table-wrap log-wrap" ref={scrollRef}>
                {rows.length === 0 && (
                  <div className="empty">
                    <Search />
                    <b>没有匹配的请求</b>
                    <span>
                      {events.length
                        ? "换一个动作筛选或关键字。"
                        : "打开“记录每条连接”后，应用的新连接会实时出现在这里。"}
                    </span>
                  </div>
                )}
                {vrows.length > 0 && (
                  <div style={{ height: virt.getTotalSize(), position: "relative" }}>
                    {virt.getVirtualItems().map((vi) => {
                      const e = vrows[vi.index]!;
                      const host = displayHost(e, view.flows);
                      const act = eventAction(e);
                      return (
                        <div
                          key={e.seq}
                          className="row log-row"
                          style={{
                            position: "absolute",
                            top: 0,
                            left: 0,
                            right: 0,
                            height: 40,
                            transform: `translateY(${vi.start}px)`,
                            gridTemplateColumns: LOG_COLS,
                          }}
                          onContextMenu={(ev) => {
                            ev.preventDefault();
                            openMenu(e, ev.clientX, ev.clientY);
                          }}
                        >
                          <span className="mono dim">{fmtTime(e.time)}</span>
                          <span>
                            {e.kind === "conn" ? (
                              <span className={clsx("chip", matchChipClass(act))}>
                                {ACTION_NAMES[act]}
                              </span>
                            ) : (
                              <span className="chip muted">事件</span>
                            )}
                          </span>
                          <span className="proc-name" title={e.process}>
                            {e.process || "—"}
                          </span>
                          <span className="mono target" title={host}>
                            {host || "—"}
                          </span>
                          <span className="mono dim">{e.port || "—"}</span>
                          <span className="dsc mono" title={e.info || e.text}>
                            {e.kind === "conn" ? e.info || "" : e.text || e.info || ""}
                          </span>
                          <span className="right">
                            {e.ruleKey ? (
                              <span className="chip hit">{shortKey(e.ruleKey)}</span>
                            ) : (
                              <span className="dim">—</span>
                            )}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                )}
              </div>
            ) : (
              <div className="table-wrap log-wrap" ref={scrollRef}>
                {grouped.length === 0 && (
                  <div className="empty">
                    <Search />
                    <b>没有可以归组的请求</b>
                    <span>换一个动作筛选或关键字。</span>
                  </div>
                )}
                {grouped.map((g) => (
                  <div
                    key={g.key}
                    className="row group-row"
                    style={{ gridTemplateColumns: "74px 70px minmax(220px, 2fr) 90px 70px 1fr" }}
                    onContextMenu={(ev) => {
                      ev.preventDefault();
                      const t = g.last;
                      onMenu({
                        x: ev.clientX,
                        y: ev.clientY,
                        header: <span>{group === "process" ? "进程" : "域名"}</span>,
                        items: [
                          {
                            kind: "item",
                            label: `为 ${g.key} 建规则`,
                            icon: <GitBranch size={14} />,
                            onSelect: () =>
                              onNewRule([
                                group === "process"
                                  ? { process: g.key, pid: 0, host: "", port: 0, udp: false, proxyKey: t.proxyKey, matchedRuleKey: "" }
                                  : { process: "*", pid: 0, host: g.key, port: 0, udp: false, proxyKey: t.proxyKey, matchedRuleKey: "" },
                              ]),
                          },
                          {
                            kind: "item",
                            label: "只看这一组",
                            icon: <Search size={14} />,
                            onSelect: () => setQ(g.key),
                          },
                          {
                            kind: "item",
                            label: "复制",
                            icon: <Copy size={14} />,
                            onSelect: () => {
                              navigator.clipboard?.writeText(g.key).catch(() => undefined);
                              onToast(true, "已复制", g.key);
                            },
                          },
                        ],
                      });
                    }}
                  >
                    <span className="mono dim">{g.last ? fmtTime(g.last.time) : "—"}</span>
                    <span>
                      <span className="chip muted">{group === "process" ? "进程" : "域名"}</span>
                    </span>
                    <span className="proc-name">{g.key}</span>
                    <span className="num">{g.count} 次</span>
                    <span className="num dim">{g.sessions}</span>
                    <span className="mono dim">{g.sample}</span>
                  </div>
                ))}
              </div>
            )}

            <div className="table-foot">
              <span className="tip">
                普通用户可读：连接事件来自引擎回调，只带目标 IP；域名列在有对应会话时补全。
              </span>
              <span className="right tip">右键任意一行生成规则</span>
            </div>
          </div>
        )}
      </div>
    </>
  );
}

function filterEvents(view: View, events: LogEvent[], q: string, action: "all" | RuleAction) {
  const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
  return events.filter((e) => {
    if (e.kind === "test") return false;
    if (e.kind === "conn" && action !== "all" && eventAction(e) !== action) return false;
    if (e.kind === "log" && action !== "all") return false;
    if (!terms.length) return true;
    const host = displayHost(e, view.flows);
    const hay = `${e.process ?? ""} ${host} ${e.port ?? ""} ${e.info ?? ""} ${e.text ?? ""}`.toLowerCase();
    return terms.every((t) => hay.includes(t));
  });
}

interface LiveGroupRow {
  key: string;
  count: number;
  sessions: number;
  sample: string;
  last: LogEvent;
}

function groupEvents(
  rows: LogEvent[],
  mode: LiveGroup,
  hostOf: (e: LogEvent) => string,
): LiveGroupRow[] {
  const map = new Map<string, { row: LiveGroupRow; targets: Set<string> }>();
  for (const e of rows) {
    if (e.kind !== "conn") continue;
    const host = hostOf(e);
    const key = (mode === "process" ? e.process : host) || "—";
    let cur = map.get(key);
    if (!cur) {
      cur = {
        row: { key, count: 0, sessions: 0, sample: "", last: e },
        targets: new Set(),
      };
      map.set(key, cur);
    }
    cur.row.count += 1;
    cur.row.last = e;
    cur.row.sample = `${host || "?"}:${e.port ?? 0}`;
    cur.targets.add(`${host}:${e.port}`);
    cur.row.sessions = cur.targets.size;
  }
  return Array.from(map.values())
    .map((v) => v.row)
    .sort((a, b) => b.count - a.count);
}

// ---------------------------------------------------------------- rules

export function RulesPage({
  view,
  onEdit,
  onToast,
}: {
  view: View;
  onEdit: (r: Rule | null, insertAt: number) => void;
  onToast: Toast;
}) {
  const [q, setQ] = useState("");
  const [dragKey, setDragKey] = useState<string | null>(null);
  const [overKey, setOverKey] = useState<string | null>(null);
  const [del, setDel] = useState<Rule | null>(null);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [confirmBatch, setConfirmBatch] = useState(false);

  const rules = useMemo(() => {
    const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length) return view.rules;
    return view.rules.filter((r) =>
      terms.every((t) => describeRule(r, view.proxies).toLowerCase().includes(t)),
    );
  }, [view.rules, view.proxies, q]);

  const chosen = rules.filter((r) => picked.has(r.key));

  const move = async (fromKey: string, toKey: string) => {
    if (fromKey === toKey) return;
    const keys = view.rules.map((r) => r.key);
    const from = keys.indexOf(fromKey);
    const to = keys.indexOf(toKey);
    if (from < 0 || to < 0) return;
    const next = keys.slice();
    next.splice(from, 1);
    next.splice(to, 0, fromKey);
    try {
      await api.reorderRules(next);
      onToast(true, "顺序已更新", `第 ${to + 1} 位`);
    } catch (e) {
      onToast(false, "排序失败", String(e));
    }
  };

  const deletePicked = async () => {
    const list = chosen;
    try {
      for (const r of list) await api.deleteRule(r.key);
      setPicked(new Set());
      setConfirmBatch(false);
      onToast(true, `已删除 ${list.length} 条规则`);
    } catch (e) {
      onToast(false, "批量删除失败", String(e));
    }
  };

  const cols = "28px 26px 34px minmax(150px, 1.1fr) minmax(190px, 1.9fr) 82px 112px 96px";

  return (
    <>
      <div className="page-head">
        <div>
          <h1>规则</h1>
          <div className="sub">自上而下匹配，命中第一条即生效；拖动排序立即落盘</div>
        </div>
        <div className="right">
          <div className="search" style={{ width: 220 }}>
            <Search />
            <input
              className="input"
              placeholder="搜索规则"
              value={q}
              onChange={(e) => setQ(e.target.value)}
            />
          </div>
          <button className="btn primary" onClick={() => onEdit(null, view.rules.length + 1)}>
            <Plus size={14} /> 新建规则
          </button>
        </div>
      </div>
      <div className="page-body">
        <div className="card">
          {picked.size > 0 && (
            <div className="selbar">
              <b>已选 {picked.size} 条</b>
              <button className="btn sm danger" onClick={() => setConfirmBatch(true)}>
                <Trash2 size={13} /> 批量删除
              </button>
              <button className="btn sm ghost right" onClick={() => setPicked(new Set())}>
                取消选择
              </button>
            </div>
          )}
          <div className="rule-head" style={{ gridTemplateColumns: cols }}>
            <button
              className={clsx("pick", picked.size > 0 && "on")}
              title="全选"
              onClick={() =>
                setPicked(rules.every((r) => picked.has(r.key)) ? new Set() : new Set(rules.map((r) => r.key)))
              }
            />
            <span />
            <span>#</span>
            <span>进程</span>
            <span>目标</span>
            <span>协议</span>
            <span>动作</span>
            <span className="right">启用</span>
          </div>
          {rules.length === 0 && (
            <div className="empty">
              <Network />
              <b>{q ? "没有匹配的规则" : "还没有规则"}</b>
              <span>从“连接”或“实时日志”右键任意一条，一步生成规则。</span>
            </div>
          )}
          {rules.map((r) => {
            const pos = view.rules.findIndex((x) => x.key === r.key) + 1;
            const p = view.proxies.find((x) => x.key === r.proxyKey);
            const sel = picked.has(r.key);
            return (
              <div
                key={r.key}
                className={clsx(
                  "rule",
                  !r.enabled && "disabled",
                  sel && "sel",
                  dragKey === r.key && "dragging",
                  overKey === r.key && "over",
                )}
                style={{ gridTemplateColumns: cols }}
                draggable
                onDragStart={(e) => {
                  setDragKey(r.key);
                  e.dataTransfer.effectAllowed = "move";
                }}
                onDragOver={(e) => {
                  e.preventDefault();
                  setOverKey(r.key);
                }}
                onDragLeave={() => setOverKey((k) => (k === r.key ? null : k))}
                onDrop={(e) => {
                  e.preventDefault();
                  setOverKey(null);
                  if (dragKey) void move(dragKey, r.key);
                  setDragKey(null);
                }}
                onDragEnd={() => {
                  setDragKey(null);
                  setOverKey(null);
                }}
              >
                <button
                  className={clsx("pick", sel && "on")}
                  onClick={() =>
                    setPicked((cur) => {
                      const next = new Set(cur);
                      if (next.has(r.key)) next.delete(r.key);
                      else next.add(r.key);
                      return next;
                    })
                  }
                />
                <span className="grip">
                  <GripVertical size={13} />
                </span>
                <span className="num dim">{pos}</span>
                <div className="proc">
                  <div className="nm mono">{r.process || "*"}</div>
                  {r.localhost && <div className="pid">含 localhost</div>}
                </div>
                <div className="rule-target">
                  <div className="mono">{targetsLabel(r)}</div>
                  {r.note && <div className="tip">{r.note}</div>}
                </div>
                <span className="mono dim">{PROTO_NAMES[r.protocol]}</span>
                <span className={clsx("chip", r.action === 0 ? "proxy" : r.action === 1 ? "direct" : "block")}>
                  {r.action === 0 ? p?.name || "代理" : ACTION_NAMES[r.action]}
                </span>
                <div className="right actions">
                  <Toggle
                    on={r.enabled}
                    onChange={() =>
                      api
                        .toggleRule(r.key, !r.enabled)
                        .catch((e) => onToast(false, "切换失败", String(e)))
                    }
                  />
                  <button className="btn icon ghost sm" onClick={() => onEdit(r, 0)} title="编辑">
                    <Pencil size={13} />
                  </button>
                  <button className="btn icon ghost sm" onClick={() => setDel(r)} title="删除">
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
            );
          })}
          <div className="table-foot">
            <span className="tip">没有命中任何规则时默认直连。禁用规则保留位置但不参与匹配。</span>
            <span className="right tip">{view.rules.length} 条规则</span>
          </div>
        </div>
      </div>
      {del && (
        <Confirm
          title="删除规则"
          body={
            <>
              确定删除 <b>{describeRule(del, view.proxies)}</b>？引擎会立即停止使用它。
            </>
          }
          onConfirm={async () => {
            try {
              await api.deleteRule(del.key);
              setPicked((cur) => {
                const next = new Set(cur);
                next.delete(del.key);
                return next;
              });
              onToast(true, "已删除规则");
            } catch (e) {
              onToast(false, "删除失败", String(e));
            }
          }}
          onClose={() => setDel(null)}
        />
      )}
      {confirmBatch && (
        <Confirm
          title={`删除 ${chosen.length} 条规则`}
          body={<>{chosen.slice(0, 4).map((r) => describeRule(r, view.proxies)).join("；")}{chosen.length > 4 ? " …" : ""}</>}
          confirmLabel="全部删除"
          onConfirm={deletePicked}
          onClose={() => setConfirmBatch(false)}
        />
      )}
    </>
  );
}

function targetsLabel(r: Rule) {
  const parts: string[] = [];
  const port = r.ports && r.ports !== "*" ? `端口 ${r.ports}` : "";
  if (r.domains) parts.push(r.domains);
  if (r.hosts && r.hosts !== "*") parts.push(r.hosts);
  if (!parts.length) return port ? `所有主机 · ${port}` : "全部流量";
  if (port) parts.push(port);
  return parts.join(" · ");
}


// ---------------------------------------------------------------- proxies

export function ProxiesPage({
  view,
  onEdit,
  onToast,
}: {
  view: View;
  onEdit: (p: Proxy | null) => void;
  onToast: Toast;
}) {
  const [run, setRun] = useState<TestRun | null>(null);
  const [del, setDel] = useState<Proxy | null>(null);

  useEffect(() => {
    setRun(view.test ?? null);
  }, [view.test]);

  const startTest = async (p: Proxy) => {
    try {
      await api.testProxy(p.key);
      onToast(true, "开始测试", `${p.ip}:${p.port}`);
    } catch (e) {
      onToast(false, "测试失败", String(e));
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>代理</h1>
          <div className="sub">最多 32 个配置 · 规则按 key 引用 · UDP 仅 SOCKS5 生效</div>
        </div>
        <div className="right">
          <button className="btn primary" onClick={() => onEdit(null)}>
            <Plus size={14} /> 新增代理
          </button>
        </div>
      </div>
      <div className="page-body">
        {view.proxies.length === 0 && (
          <div className="card">
            <div className="empty">
              <Server />
              <b>还没有代理配置</b>
              <span>添加一个本地 SOCKS5（例如 clash 的 127.0.0.1:7890）即可开始。</span>
            </div>
          </div>
        )}
        <div className="proxy-grid">
          {view.proxies.map((p) => {
            const used = view.rules.filter((r) => r.proxyKey === p.key && r.action === 0).length;
            return (
              <div key={p.key} className="card proxy-card">
                <div className="proxy-top">
                  <span className={clsx("type-ico", p.type === 1 ? "socks" : "http")}>
                    {p.type === 1 ? <PlugZap size={15} /> : <Globe size={15} />}
                  </span>
                  <div style={{ minWidth: 0 }}>
                    <div className="nm">{p.name || "未命名代理"}</div>
                    <div className="mono addr">
                      {p.ip}:{p.port}
                    </div>
                  </div>
                  <span className="chip muted right">{PROXY_NAMES[p.type]}</span>
                </div>
                <div className="proxy-meta">
                  <span>{used} 条规则使用</span>
                  {p.user ? <span>有认证</span> : <span>无认证</span>}
                  {p.sendDomain ? <span>远程 DNS</span> : <span>本地解析</span>}
                </div>
                <div className="proxy-actions">
                  <button className="btn sm" onClick={() => startTest(p)} disabled={run?.running}>
                    {run?.running && run.proxyKey === p.key ? (
                      <Loader2 className="spin" size={13} />
                    ) : (
                      <Play size={13} />
                    )}
                    测试
                  </button>
                  <button className="btn sm" onClick={() => onEdit(p)}>
                    <Pencil size={13} /> 编辑
                  </button>
                  <button className="btn sm ghost right" onClick={() => setDel(p)}>
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
            );
          })}
        </div>

        {run && (run.lines.length > 0 || run.running) && (
          <div className="card">
            <div className="card-head">
              <b>连通性测试</b>
              <span className="mono tip">{run.target}</span>
              {run.running ? (
                <span className="chip muted right">
                  <Loader2 className="spin" size={12} /> 进行中
                </span>
              ) : (
                <span className={clsx("chip right", run.result === 0 ? "direct" : "block")}>
                  {run.result === 0 ? <Check size={12} /> : <X size={12} />}
                  {run.result === 0 ? "通过" : `失败 ${run.result ?? ""}`}
                </span>
              )}
              <button className="btn icon ghost sm" onClick={() => setRun(null)}>
                <X size={13} />
              </button>
            </div>
            <div className="testlog">
              {run.lines.map((l, i) => (
                <div key={i} className="logline">
                  <span className="t mono">{String(i + 1).padStart(2, "0")}</span>
                  <span className="dsc mono">{l}</span>
                </div>
              ))}
              {run.running && (
                <div className="logline">
                  <span className="t mono">··</span>
                  <span className="dsc dim">等待代理回应…</span>
                </div>
              )}
            </div>
          </div>
        )}
      </div>
      {del && (
        <Confirm
          title="删除代理"
          body={
            <>
              删除 <b>{del.name || `${del.ip}:${del.port}`}</b> 后，引用它的规则会回退到第一个代理。
            </>
          }
          onConfirm={async () => {
            try {
              await api.deleteProxy(del.key);
              onToast(true, "已删除代理");
            } catch (e) {
              onToast(false, "删除失败", String(e));
            }
          }}
          onClose={() => setDel(null)}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------- settings

export function SettingsPage({
  view,
  status,
  onStatus,
  onToast,
}: {
  view: View;
  status: AppStatus | null;
  onStatus: (connect?: boolean) => void;
  onToast: Toast;
}) {
  const [busy, setBusy] = useState<"" | "install" | "uninstall" | "connect" | "login">("");
  const s = status?.service;
  const engineOk = status?.engineReady ?? false;

  const act = async (kind: "install" | "uninstall" | "connect" | "login", on?: boolean) => {
    setBusy(kind);
    try {
      if (kind === "install") {
        await api.installEngine();
        onToast(true, "引擎服务已安装", "正在连接引擎…");
        try {
          await api.connect();
          onToast(true, "已连接引擎");
        } catch (e) {
          onToast(false, "服务已安装，但连接失败", String(e));
        }
        onStatus(true);
      } else if (kind === "uninstall") {
        await api.uninstallEngine();
        onToast(true, "引擎服务已卸载");
        onStatus(false);
      } else if (kind === "connect") {
        await api.connect();
        onToast(true, "已连接引擎");
        onStatus(true);
      } else {
        await api.setAutoStart(!!on);
        onToast(true, on ? "已设置开机自启" : "已关闭开机自启");
        onStatus();
      }
    } catch (e) {
      const title =
        kind === "install" ? "安装失败" : kind === "uninstall" ? "卸载失败" : kind === "connect" ? "连接失败" : "设置失败";
      onToast(false, title, String(e));
    } finally {
      setBusy("");
    }
  };

  return (
    <>
      <div className="page-head">
        <div>
          <h1>设置</h1>
          <div className="sub">引擎服务、数据位置与行为偏好</div>
        </div>
        <div className="right">
          <button className="btn" onClick={() => onStatus()}>
            <RefreshCw size={14} /> 刷新状态
          </button>
        </div>
      </div>
      <div className="page-body settings-grid">
        <div className="card">
          <div className="card-head">
            <b>引擎</b>
            {status?.admin ? (
              <span className="chip direct right">
                <ShieldCheck size={12} /> 管理员
              </span>
            ) : (
              <span className="chip muted right">普通权限</span>
            )}
          </div>
          <div className="card-body">
            <div className="kv">
              <span>服务</span>
              <b className={clsx(s?.installed ? (s.running ? "ok" : "warn") : "err")}>
                {s?.installed ? (s.running ? "已安装 · 运行中" : "已安装 · 已停止") : "未安装"}
              </b>
            </div>
            <div className="kv">
              <span>连接</span>
              <b className={view.connected ? "ok" : "err"}>
                {view.connected ? `已连接 · 协议 v${view.version}` : "未连接"}
              </b>
            </div>
            <div className="kv">
              <span>启动类型</span>
              <b>{s?.startType || "—"}</b>
            </div>
            <div className="kv">
              <span>引擎文件</span>
              <b className={engineOk ? "ok" : "err"}>{engineOk ? "完整" : "缺失"}</b>
            </div>
            {s?.binPath && (
              <div className="kv">
                <span>服务路径</span>
                <b className="mono small">{s.binPath}</b>
              </div>
            )}
            {s?.error && (
              <div className="kv">
                <span>错误</span>
                <b className="mono small err">{s.error}</b>
              </div>
            )}
            <div className="row-actions">
              <button className="btn primary" disabled={!!busy || !engineOk} onClick={() => act("install")}>
                {busy === "install" ? <Loader2 className="spin" size={14} /> : <ShieldCheck size={14} />}
                {s?.installed ? "修复 / 重启服务" : "安装引擎服务"}
              </button>
              <button className="btn" disabled={!!busy} onClick={() => act("connect")}>
                {busy === "connect" ? <Loader2 className="spin" size={14} /> : <Power size={14} />}
                连接引擎
              </button>
              {s?.installed && (
                <button className="btn danger" disabled={!!busy} onClick={() => act("uninstall")}>
                  {busy === "uninstall" ? <Loader2 className="spin" size={14} /> : <Trash2 size={14} />}
                  卸载
                </button>
              )}
            </div>
            <div className="tip">
              安装需要管理员权限（会弹出 UAC）。服务名固定为 ProxyBridgeSvc，与官方 ProxyBridge 共存时会被替换。
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <b>行为</b>
          </div>
          <div className="card-body">
            <Pref
              title="启用拦截"
              desc="关闭后所有流量直连（规则仍保留）"
              on={view.enabled}
              onChange={(v) => api.setEnabled(v).catch((e) => onToast(false, "切换失败", String(e)))}
            />
            <Pref
              title="记录每条连接"
              desc="把新建连接写入实时日志"
              on={view.trafficLog}
              onChange={(v) => api.setTrafficLog(v).catch((e) => onToast(false, "切换失败", String(e)))}
            />
            <Pref
              title="全量流量日志"
              desc="引擎调试日志级别，会明显变吵"
              on={view.logAll}
              onChange={(v) => api.setLogAll(v).catch((e) => onToast(false, "切换失败", String(e)))}
            />
            <Pref
              title="开机自启"
              desc="随 Windows 登录启动，最小化到托盘"
              on={!!status?.autoStart}
              busy={busy === "login"}
              onChange={(v) => act("login", v)}
            />
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <b>位置</b>
            <button className="btn sm right" onClick={() => api.revealData()}>
              <FolderOpen size={13} /> 打开
            </button>
          </div>
          <div className="card-body">
            <div className="kv">
              <span>配置目录</span>
              <b className="mono small">{status?.dataDir || "—"}</b>
            </div>
            <div className="kv">
              <span>引擎目录</span>
              <b className="mono small">{status?.engineDir || "（未找到）"}</b>
            </div>
            <div className="kv">
              <span>数据文件</span>
              <b className="mono small">{status?.settingsPath || "—"}</b>
            </div>
          </div>
        </div>

        <div className="card">
          <div className="card-head">
            <b>关于</b>
            <span className="chip muted right">MIT</span>
          </div>
          <div className="card-body">
            <div className="kv">
              <span>PBDeck</span>
              <b>{status?.version || "0.1.0"}</b>
            </div>
            <div className="kv">
              <span>平台</span>
              <b className="mono small">{status?.platform || "—"}</b>
            </div>
            <div className="kv">
              <span>引擎</span>
              <b>ProxyBridge 4.3.0（复用）</b>
            </div>
            <div className="tip">
              内核由 InterceptSuite/ProxyBridge 提供（WinDivert），PBDeck 只做壳与规则编排。
            </div>
          </div>
        </div>
      </div>
    </>
  );
}

function Pref({
  title,
  desc,
  on,
  busy,
  onChange,
}: {
  title: string;
  desc: string;
  on: boolean;
  busy?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="pref">
      <div>
        <b>{title}</b>
        <span className="tip">{desc}</span>
      </div>
      {busy ? <Loader2 className="spin" size={16} /> : <Toggle on={on} onChange={onChange} />}
    </div>
  );
}

// ---------------------------------------------------------------- rule drawer host

export function RuleDrawer({
  open,
  initial,
  insertAt,
  proxies,
  onClose,
  onToast,
}: {
  open: boolean;
  initial: Rule | null;
  insertAt: number;
  proxies: Proxy[];
  onClose: () => void;
  onToast: Toast;
}) {
  if (!open) return null;
  return (
    <RuleEditor
      initial={initial ?? undefined}
      proxies={proxies}
      insertAt={insertAt}
      onClose={onClose}
      onSave={async (r, at) => {
        try {
          await api.saveRule(r, at);
          onToast(true, initial ? "规则已更新" : "规则已创建", describeRule(r, proxies));
        } catch (e) {
          onToast(false, "保存失败", String(e));
          throw e;
        }
      }}
    />
  );
}

// ---------------------------------------------------------------- small helpers

function shortKey(k: string) {
  return k.length > 7 ? `#${k.slice(0, 5)}` : `#${k}`;
}

function rootDomain(host: string) {
  const parts = host.replace(/^www\./, "").split(".");
  if (parts.length <= 2) return `*.${host}`;
  return `*.${parts.slice(-2).join(".")}`;
}
