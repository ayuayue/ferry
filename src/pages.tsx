import { useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import {
  Activity,
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
  fmtAge,
  fmtBytes,
  fmtTime,
  isIP,
  type AppStatus,
  type Flow,
  type LogEvent,
  type Proxy,
  type Rule,
  type RuleTarget,
  type TestRun,
  type View,
} from "./bridge";
import { Confirm, RuleEditor, Toggle, type MenuItem, type MenuState } from "./components";

type Toast = (ok: boolean, title: string, detail?: string) => void;

const FLOW_COLS = "14px minmax(130px, 1.1fr) minmax(160px, 1.5fr) 56px minmax(96px, .9fr) minmax(96px, .9fr) 78px 78px 56px";

// ---------------------------------------------------------------- connections

export function ConnectionsPage({
  view,
  events,
  selected,
  setSelected,
  logOpen,
  onLogToggle,
  onMenu,
  onToast,
  onNewRule,
}: {
  view: View;
  events: LogEvent[];
  selected: number[];
  setSelected: (v: number[]) => void;
  logOpen: boolean;
  onLogToggle: (v: boolean) => void;
  onMenu: (m: MenuState) => void;
  onToast: Toast;
  onNewRule: (targets: RuleTarget[], mode?: "quick" | "custom") => void;
}) {
  const [q, setQ] = useState("");
  const [udpOnly, setUdpOnly] = useState(false);
  const rows = useMemo(() => filterFlows(view, q, udpOnly), [view.flows, q, udpOnly]);
  const scrollRef = useRef<HTMLDivElement>(null);
  const virt = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => 46,
    overscan: 14,
  });

  const rowsRef = useRef(rows);
  rowsRef.current = rows;
  const selectedRef = useRef(selected);
  selectedRef.current = selected;

  useEffect(() => {
    // drop selection of flows that vanished
    if (!selected.length) return;
    const alive = new Set(view.flows.map((f) => f.id));
    const next = selected.filter((id) => alive.has(id));
    if (next.length !== selected.length) setSelected(next);
  }, [view.flows, selected, setSelected]);

  const targetsFor = (f: Flow): RuleTarget => ({
    flowId: f.id,
    process: f.name,
    pid: f.pid,
    host: f.host,
    port: f.port,
    udp: f.udp,
    proxyKey: f.proxyKey,
    matchedRuleKey: f.ruleKey,
  });

  const openMenu = (f: Flow, x: number, y: number) => {
    const cur = selectedRef.current;
    const ids = cur.includes(f.id) ? cur : [f.id];
    if (!cur.includes(f.id)) setSelected([f.id]);
    const flows = rowsRef.current.filter((r) => ids.includes(r.id));
    const targets = flows.map(targetsFor);
    const multi = targets.length > 1;
    const host = f.host;
    const items: MenuItem[] = [
      { kind: "label", text: `${f.name || "未知进程"} → ${host || "?"}:${f.port}` },
      {
        kind: "item",
        label: multi ? `建规则（${targets.length} 条连接）` : "建规则：仅此主机",
        icon: <Zap size={14} />,
        hint: "此连接",
        onSelect: () => onNewRule(targets),
      },
      {
        kind: "item",
        label: `建规则：${f.name || "该进程"} 的所有流量`,
        icon: <GitBranch size={14} />,
        hint: "整进程",
        onSelect: () => onNewRule([{ ...targets[0]!, host: "", port: 0, flowId: 0 }]),
      },
    ];
    if (host && !isIP(host)) {
      items.push({
        kind: "item",
        label: `建规则：所有应用访问 ${rootDomain(host)}`,
        icon: <Globe size={14} />,
        onSelect: () =>
          onNewRule([{ ...targets[0]!, process: "*", port: 0, flowId: 0, host: rootDomain(host) }]),
      });
    }
    items.push(
      { kind: "sep" },
      {
        kind: "item",
        label: `让 ${host || "此主机"} 直连`,
        icon: <Route size={14} />,
        onSelect: async () => {
          try {
            await api.rulesFromTargets(
              [{ ...targets[0]!, process: "*" }],
              { scope: "host", action: 1, proxyKey: "", ports: "*", localhost: false, moveToTop: false },
            );
            onToast(true, "已添加直连规则", host);
          } catch (e) {
            onToast(false, "添加失败", String(e));
          }
        },
      },
      {
        kind: "item",
        danger: true,
        label: `阻断 ${host || "此主机"}`,
        icon: <Ban size={14} />,
        onSelect: async () => {
          try {
            await api.rulesFromTargets(
              [{ ...targets[0]!, process: "*" }],
              { scope: "host", action: 2, proxyKey: "", ports: "*", localhost: false, moveToTop: false },
            );
            onToast(true, "已添加阻断规则", host);
          } catch (e) {
            onToast(false, "添加失败", String(e));
          }
        },
      },
      { kind: "sep" },
      {
        kind: "item",
        label: "复制主机名",
        icon: <Copy size={14} />,
        onSelect: () => {
          navigator.clipboard?.writeText(host).catch(() => undefined);
          onToast(true, "已复制", host);
        },
      },
      {
        kind: "item",
        label: "选中所有同进程连接",
        icon: <Activity size={14} />,
        onSelect: () =>
          setSelected(rowsRef.current.filter((r) => r.pid === f.pid).map((r) => r.id)),
      },
    );
    onMenu({ x, y, items, header: <span>连接 #{f.id}</span> });
  };

  const total = view.stats.events + view.stats.flows;

  return (
    <>
      <div className="page-head">
        <div>
          <h1>连接</h1>
          <div className="sub">
            实时流与拦截明细 · 右键任意连接可一键转成规则
          </div>
        </div>
        <div className="right">
          <button className="btn" onClick={() => onLogToggle(!logOpen)}>
            <Activity size={14} />
            {logOpen ? "隐藏日志" : "查看日志"}
            {events.length ? <span className="chip muted">{events.length}</span> : null}
          </button>
          <button className="btn" onClick={() => api.clearEvents()}>
            <Trash2 size={14} /> 清空
          </button>
        </div>
      </div>

      <div className="page-body">
        <div className="kpis">
          <Kpi icon={<Cable />} label="活动连接" value={String(view.flows.length)} foot={`累计 ${total}`} />
          <Kpi icon={<ArrowUpRight />} label="上行" value={fmtBytes(view.stats.up)} foot="本次会话" />
          <Kpi icon={<ArrowUpRight className="down" />} label="下行" value={fmtBytes(view.stats.down)} foot="本次会话" />
          <Kpi
            icon={<ShieldCheck />}
            label="拦截状态"
            value={view.enabled ? "运行中" : "已暂停"}
            foot={view.mode === "mock" ? "演示引擎" : view.mode === "service" ? "引擎服务" : "未连接"}
            tone={view.enabled ? "mint" : "coral"}
          />
        </div>

        <div className="card table-card">
          <div className="toolbar">
            <div className="search grow">
              <Search />
              <input
                className="input"
                placeholder="筛选进程、主机、端口…"
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
            </div>
            <button
              className={clsx("btn sm", udpOnly && "active")}
              onClick={() => setUdpOnly(!udpOnly)}
            >
              <Route size={13} /> 只看 UDP
            </button>
            <div className="live-strip">
              <span className={view.connected ? "pulse" : "dot"} />
              {view.connected ? "实时" : "离线"}
            </div>
            <span className="num tip">{rows.length} 行</span>
          </div>

          {selected.length > 0 && (
            <div className="selbar">
              <b>已选 {selected.length} 条</b>
              <button
                className="btn sm primary"
                onClick={() => {
                  const flows = rows.filter((r) => selected.includes(r.id));
                  onNewRule(flows.map(targetsFor));
                }}
              >
                <Zap size={13} /> 生成规则
              </button>
              <button
                className="btn sm"
                onClick={() => {
                  const flows = rows.filter((r) => selected.includes(r.id));
                  const ps = Array.from(new Set(flows.map((f) => f.name).filter(Boolean)));
                  onNewRule(
                    ps.map((p) => ({
                      flowId: 0,
                      process: p,
                      pid: 0,
                      host: "",
                      port: 0,
                      udp: false,
                      proxyKey: "",
                      matchedRuleKey: "",
                    })),
                  );
                }}
              >
                <GitBranch size={13} /> 按进程
              </button>
              <button className="btn sm ghost right" onClick={() => setSelected([])}>
                取消选择
              </button>
            </div>
          )}

          <div className="table-head" style={{ gridTemplateColumns: FLOW_COLS }}>
            <span />
            <span>进程</span>
            <span>目标</span>
            <span>端口</span>
            <span>规则</span>
            <span>代理</span>
            <span className="right">上行</span>
            <span className="right">下行</span>
            <span className="right">时间</span>
          </div>

          <div className="table-wrap" ref={scrollRef}>
            {rows.length === 0 && (
              <div className="empty">
                {view.connected ? <Search /> : <PlugZap />}
                <b>{view.connected ? "还没有匹配的连接" : "引擎未连接"}</b>
                <span>
                  {view.connected
                    ? "让应用产生一些流量，或换一个筛选条件。"
                    : view.mode === "mock"
                      ? "点击右上角切换引擎，或重新连接。"
                      : "在设置页安装并启动引擎服务，然后点击“连接”。"}
                </span>
              </div>
            )}
            {rows.length > 0 && (
              <div style={{ height: virt.getTotalSize(), position: "relative" }}>
                {virt.getVirtualItems().map((vi) => {
                  const f = rows[vi.index]!;
                  const sel = selected.includes(f.id);
                  return (
                    <div
                      key={f.id}
                      className={clsx("row", sel && "sel")}
                      style={{
                        position: "absolute",
                        top: 0,
                        left: 0,
                        right: 0,
                        height: 46,
                        transform: `translateY(${vi.start}px)`,
                        gridTemplateColumns: FLOW_COLS,
                      }}
                      onClick={(e) => {
                        if (e.ctrlKey || e.metaKey) {
                          setSelected(sel ? selected.filter((x) => x !== f.id) : [...selected, f.id]);
                        } else setSelected([f.id]);
                      }}
                      onContextMenu={(e) => {
                        e.preventDefault();
                        openMenu(f, e.clientX, e.clientY);
                      }}
                    >
                      <span className={clsx("dot", !f.udp && "tcp")} />
                      <div className="proc">
                        <div className="nm">
                          {f.name || "未知进程"}
                          {f.udp ? <span className="badge">UDP</span> : null}
                        </div>
                        <div className="pid">PID {f.pid || "—"}</div>
                      </div>
                      <div className="target mono" title={f.host}>
                        {f.host || "—"}
                        {f.ipv6 ? <span className="badge">v6</span> : null}
                      </div>
                      <div className="mono dim">{f.port}</div>
                      <div>
                        <span className={clsx("chip", ruleTone(f.ruleId))} title={f.ruleKey}>
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
                  );
                })}
              </div>
            )}
          </div>

          <div className="table-foot">
            <span className="tip">
              每秒轮询一次；仅显示走代理的连接，直连与阻断的请求会出现在“日志”里。
            </span>
            <span className="right tip">选中多行后右键可批量生成规则</span>
          </div>
        </div>
      </div>

      {logOpen && (
        <LogDrawer
          events={events}
          onClose={() => onLogToggle(false)}
          onMenu={onMenu}
          onToast={onToast}
          onNewRule={onNewRule}
        />
      )}
    </>
  );
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

function filterFlows(view: View, q: string, udpOnly: boolean) {
  const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
  return view.flows.filter((f) => {
    if (udpOnly && !f.udp) return false;
    if (!terms.length) return true;
    const hay = `${f.name} ${f.host} ${f.port} ${f.pid} ${f.proxyName}`.toLowerCase();
    return terms.every((t) => hay.includes(t));
  });
}

function ruleTone(id: number) {
  if (!id) return "muted";
  return "hit";
}

function shortKey(k: string) {
  return k.length > 7 ? `#${k.slice(0, 5)}` : `#${k}`;
}

function rootDomain(host: string) {
  const parts = host.replace(/^www\./, "").split(".");
  if (parts.length <= 2) return `*.${host}`;
  return `*.${parts.slice(-2).join(".")}`;
}

// ---------------------------------------------------------------- log drawer

function LogDrawer({
  events,
  onClose,
  onMenu,
  onToast,
  onNewRule,
}: {
  events: LogEvent[];
  onClose: () => void;
  onMenu: (m: MenuState) => void;
  onToast: Toast;
  onNewRule: (targets: RuleTarget[], mode?: "quick" | "custom") => void;
}) {
  const [q, setQ] = useState("");
  const [only, setOnly] = useState<"all" | "conn" | "session">("all");
  const list = useMemo(() => {
    const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
    return events
      .filter((e) => (only === "all" ? true : only === "conn" ? e.kind === "conn" : e.kind !== "conn"))
      .filter((e) => {
        if (!terms.length) return true;
        const hay = `${e.process} ${e.host} ${e.text} ${e.info} ${e.port}`.toLowerCase();
        return terms.every((t) => hay.includes(t));
      });
  }, [events, q, only]);

  return (
    <section className="drawer">
      <div className="drawer-head">
        <Activity size={15} />
        <b>实时日志</b>
        <div className="search" style={{ width: 260, marginLeft: 12 }}>
          <Search />
          <input
            className="input"
            placeholder="筛选日志"
            value={q}
            onChange={(e) => setQ(e.target.value)}
          />
        </div>
        <div className="seg">
          {[
            { k: "all", label: "全部" },
            { k: "conn", label: "连接" },
            { k: "session", label: "会话" },
          ].map((t) => (
            <button
              key={t.k}
              className={clsx(only === t.k && "on")}
              onClick={() => setOnly(t.k as typeof only)}
            >
              {t.label}
            </button>
          ))}
        </div>
        <span className="tip right num">{list.length} 条</span>
        <button className="btn icon ghost sm" onClick={onClose}>
          <ChevronDown size={16} />
        </button>
      </div>
      <div className="drawer-body">
        {list.length === 0 && (
          <div className="empty">
            <Activity />
            <b>暂无日志</b>
            <span>连接与引擎事件会实时出现在这里，右键可生成规则。</span>
          </div>
        )}
        {list.slice(0, 500).map((e) => (
          <div
            key={e.seq}
            className={clsx("logline", e.kind)}
            onContextMenu={(ev) => {
              ev.preventDefault();
              const target: RuleTarget = {
                flowId: 0,
                process: e.process ?? "",
                pid: e.pid ?? 0,
                host: e.host ?? "",
                port: e.port ?? 0,
                udp: false,
                proxyKey: e.proxyKey,
                matchedRuleKey: e.ruleKey,
              };
              onMenu({
                x: ev.clientX,
                y: ev.clientY,
                header: <span>{e.kind === "conn" ? "连接事件" : "引擎日志"}</span>,
                items: [
                  {
                    kind: "item",
                    label: `为 ${e.process || "该应用"} 建规则`,
                    icon: <Zap size={14} />,
                    hint: "进程",
                    onSelect: () => onNewRule([{ ...target, host: "", port: 0 }]),
                  },
                  {
                    kind: "item",
                    label: e.host ? `为 ${e.host} 建规则` : "为此连接建规则",
                    icon: <Globe size={14} />,
                    disabled: !e.host,
                    onSelect: () => onNewRule([target]),
                  },
                  { kind: "sep" },
                  {
                    kind: "item",
                    label: "复制主机",
                    icon: <Copy size={14} />,
                    disabled: !e.host,
                    onSelect: () => {
                      navigator.clipboard?.writeText(e.host ?? "").catch(() => undefined);
                      onToast(true, "已复制", e.host);
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
                ],
              });
            }}
          >
            <span className="t mono">{fmtTime(e.time)}</span>
            <span className={clsx("tag", e.kind)}>
              {e.kind === "conn" ? "连接" : e.kind === "test" ? "测试" : "日志"}
            </span>
            <span className="who">{e.process || "—"}</span>
            <span className="dsc mono">
              {e.kind === "conn" ? `${e.host || "?"}:${e.port}` : e.text || e.info}
            </span>
            {e.host && e.kind === "conn" && (
              <button
                className="btn icon ghost sm right quick"
                title="一键建规则"
                onClick={(ev) => {
                  ev.stopPropagation();
                  onNewRule([
                    {
                      flowId: 0,
                      process: e.process ?? "",
                      pid: e.pid ?? 0,
                      host: e.host ?? "",
                      port: e.port ?? 0,
                      udp: false,
                      proxyKey: e.proxyKey,
                      matchedRuleKey: e.ruleKey,
                    },
                  ]);
                }}
              >
                <Zap size={13} />
              </button>
            )}
          </div>
        ))}
      </div>
    </section>
  );
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

  const rules = useMemo(() => {
    const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
    if (!terms.length) return view.rules;
    return view.rules.filter((r) =>
      terms.every((t) => describeRule(r, view.proxies).toLowerCase().includes(t)),
    );
  }, [view.rules, view.proxies, q]);

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

  return (
    <>
      <div className="page-head">
        <div>
          <h1>规则</h1>
          <div className="sub">自上而下匹配，命中第一条即生效；拖动排序立即落盘</div>
        </div>
        <div className="right">
          <div className="search" style={{ width: 240 }}>
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
          <div className="rule-head" style={{ gridTemplateColumns: "26px 34px minmax(160px, 1.2fr) minmax(200px, 2fr) 90px 118px 74px" }}>
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
              <span>从“连接”页右键任意一条连接，一步生成规则。</span>
            </div>
          )}
          {rules.map((r, i) => {
            const pos = view.rules.findIndex((x) => x.key === r.key) + 1;
            const p = view.proxies.find((x) => x.key === r.proxyKey);
            return (
              <div
                key={r.key}
                className={clsx("rule", !r.enabled && "disabled", dragKey === r.key && "dragging", overKey === r.key && "over")}
                style={{ gridTemplateColumns: "26px 34px minmax(160px, 1.2fr) minmax(200px, 2fr) 90px 118px 74px" }}
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
                  <div className="tip">
                    {r.domains ? `域名 ${r.domains}` : r.hosts && r.hosts !== "*" ? `地址 ${r.hosts}` : ""}
                    {r.note ? `${r.domains || (r.hosts && r.hosts !== "*") ? " · " : ""}${r.note}` : ""}
                  </div>
                </div>
                <span className="mono dim">{PROTO_NAMES[r.protocol]}</span>
                <span className={clsx("chip", r.action === 0 ? "proxy" : r.action === 1 ? "direct" : "block")}>
                  {r.action === 0 ? p?.name || "代理" : ACTION_NAMES[r.action]}
                </span>
                <div className="right">
                  <Toggle
                    on={r.enabled}
                    onChange={() => api.toggleRule(r.key, !r.enabled).catch((e) => onToast(false, "切换失败", String(e)))}
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
              onToast(true, "已删除规则");
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

function targetsLabel(r: Rule) {
  const parts: string[] = [];
  if (r.domains) parts.push(r.domains);
  if (r.hosts && r.hosts !== "*") parts.push(r.hosts);
  if (r.ports && r.ports !== "*") parts.push(`:${r.ports}`);
  return parts.length ? parts.join(" ") : "全部流量";
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
  const [test, setTest] = useState<string>("");
  const [run, setRun] = useState<TestRun | null>(null);
  const [del, setDel] = useState<Proxy | null>(null);

  useEffect(() => {
    setRun(view.test ?? null);
  }, [view.test]);

  const startTest = async (p: Proxy) => {
    setTest(p.key);
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
              <span>添加一个本地 SOCKS5（例如 clash 的 127.0.0.1:7890/1080）即可开始。</span>
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
  onStatus: () => void;
  onToast: Toast;
}) {
  const [busy, setBusy] = useState<"" | "install" | "uninstall">("");
  const s = status?.service;
  const engineOk = status?.engineReady ?? false;

  const act = async (kind: "install" | "uninstall") => {
    setBusy(kind);
    try {
      await (kind === "install" ? api.installEngine() : api.uninstallEngine());
      onToast(
        true,
        kind === "install" ? "引擎服务已安装" : "引擎服务已卸载",
        "可能需要重启应用后生效",
      );
      onStatus();
    } catch (e) {
      onToast(false, kind === "install" ? "安装失败" : "卸载失败", String(e));
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
          <button className="btn" onClick={onStatus}>
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
              <span>启动类型</span>
              <b>{s?.startType || "—"}</b>
            </div>
            <div className="kv">
              <span>引擎文件</span>
              <b className={engineOk ? "ok" : "err"}>{engineOk ? "完整" : "缺失"}</b>
            </div>
            <div className="kv">
              <span>协议版本</span>
              <b>{view.version ? `v${view.version}` : "—"}</b>
            </div>
            {s?.binPath && (
              <div className="kv">
                <span>服务路径</span>
                <b className="mono small">{s.binPath}</b>
              </div>
            )}
            {s?.error && <div className="kv"><span>错误</span><b className="mono small err">{s.error}</b></div>}
            <div className="row-actions">
              <button
                className="btn primary"
                disabled={!!busy || !engineOk}
                onClick={() => act("install")}
              >
                {busy === "install" ? <Loader2 className="spin" size={14} /> : <ShieldCheck size={14} />}
                {s?.installed ? "修复 / 重启服务" : "安装引擎服务"}
              </button>
              {s?.installed && (
                <button className="btn danger" disabled={!!busy} onClick={() => act("uninstall")}>
                  {busy === "uninstall" ? <Loader2 className="spin" size={14} /> : <Trash2 size={14} />}
                  卸载
                </button>
              )}
              <button
                className="btn"
                onClick={() => api.connect(view.mode === "service").catch((e) => onToast(false, "连接失败", String(e)))}
              >
                <Power size={14} />
                {view.mode === "mock" ? "切换到真实引擎" : "连接到演示引擎"}
              </button>
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
              desc="把新建连接写入日志（可能很吵）"
              on={view.trafficLog}
              onChange={(v) => api.setTrafficLog(v).catch((e) => onToast(false, "切换失败", String(e)))}
            />
            <Pref
              title="全量流量日志"
              desc="引擎调试日志级别"
              on={view.logAll}
              onChange={(v) => api.setLogAll(v).catch((e) => onToast(false, "切换失败", String(e)))}
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
  onChange,
}: {
  title: string;
  desc: string;
  on: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="pref">
      <div>
        <b>{title}</b>
        <span className="tip">{desc}</span>
      </div>
      <Toggle on={on} onChange={onChange} />
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

