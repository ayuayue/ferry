import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowUpRight,
  Check,
  ChevronRight,
  CircleDot,
  FolderOpen,
  GripVertical,
  Loader2,
  Minus,
  Network,
  Pencil,
  Plus,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Square,
  Trash2,
  X,
} from "lucide-react";
import { currentWindow } from "mygo-runtime";
import clsx from "clsx";

import {
  ACTION_NAMES,
  PROTO_NAMES,
  PROXY_NAMES,
  api,
  describeRule,
  type ProcInfo,
  type Proxy,
  type Protocol,
  type Rule,
  type RuleAction,
  type View,
} from "./bridge";

// ---------- title bar ----------

function win() {
  try {
    return currentWindow;
  } catch {
    return null;
  }
}

export function TitleBar({ view, onStatus }: { view: View; onStatus?: () => void }) {
  const w = win();
  const mode =
    view.mode === "mock" ? "演示引擎" : view.mode === "service" ? "引擎服务" : "未连接";
  return (
    <header className="titlebar">
      <div className="brand">
        <span className="mark" />
        PBDeck
      </div>
      <div className="spacer" />
      <button className="engine-pill" onClick={onStatus} title="引擎状态">
        <span
          className={view.connected ? "pulse" : "dot"}
          style={
            view.connected
              ? undefined
              : { width: 7, height: 7, borderRadius: 99, background: "#61707e" }
          }
        />
        <b>{mode}</b>
        {view.connected && <span className="mono">v{view.version}</span>}
      </button>
      <div className="spacer" />
      <div className="win-buttons">
        <button onClick={() => w?.minimize()} aria-label="最小化">
          <Minus size={14} />
        </button>
        <button onClick={() => w?.toggleMaximize()} aria-label="最大化">
          <Square size={12} />
        </button>
        <button className="close" onClick={() => w?.close()} aria-label="关闭">
          <X size={15} />
        </button>
      </div>
    </header>
  );
}

// ---------- rail ----------

export type Page = "connections" | "rules" | "proxies" | "settings";

const NAV: { id: Page; label: string; icon: ReactNode }[] = [
  { id: "connections", label: "连接", icon: <Activity /> },
  { id: "rules", label: "规则", icon: <Network /> },
  { id: "proxies", label: "代理", icon: <Server /> },
  { id: "settings", label: "设置", icon: <Settings2 /> },
];

export function Rail({
  page,
  onPage,
  view,
}: {
  page: Page;
  onPage: (p: Page) => void;
  view: View;
}) {
  const counts: Record<Page, number | null> = {
    connections: view.flows.length || null,
    rules: view.rules.length || null,
    proxies: view.proxies.length || null,
    settings: null,
  };
  return (
    <nav className="rail">
      <div className="group-label">控制台</div>
      {NAV.map((n) => (
        <button
          key={n.id}
          className={clsx("nav-item", page === n.id && "active")}
          onClick={() => onPage(n.id)}
        >
          {n.icon}
          {n.label}
          {counts[n.id] ? <span className="count">{counts[n.id]}</span> : null}
        </button>
      ))}
      <div className="rail-foot">
        <div className="row">
          <span>活动连接</span>
          <b>{view.flows.length}</b>
        </div>
        <div className="row">
          <span>上行</span>
          <b>{fmt(view.stats.up)}</b>
        </div>
        <div className="row">
          <span>下行</span>
          <b>{fmt(view.stats.down)}</b>
        </div>
      </div>
    </nav>
  );
}

function fmt(n: number) {
  if (!n) return "0";
  const u = ["B", "K", "M", "G"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n >= 10 ? Math.round(n) : n.toFixed(1)}${u[i]}`;
}

// ---------- small primitives ----------

export function Toggle({ on, onChange, title }: { on: boolean; onChange: (v: boolean) => void; title?: string }) {
  return (
    <button
      className={clsx("switch", on && "on")}
      onClick={() => onChange(!on)}
      title={title}
      aria-pressed={on}
    />
  );
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: ReactNode;
}) {
  return (
    <div className="field">
      <label>{label}</label>
      {children}
      {hint && <span className="hint">{hint}</span>}
    </div>
  );
}

export function Seg<T extends string | number>({
  value,
  options,
  onChange,
}: {
  value: T;
  options: { value: T; label: string; dot?: string }[];
  onChange: (v: T) => void;
}) {
  return (
    <div className="seg">
      {options.map((o) => (
        <button
          key={String(o.value)}
          className={clsx(o.value === value && "on")}
          onClick={() => onChange(o.value)}
        >
          {o.dot && <span className="dot" style={{ background: o.dot }} />}
          {o.label}
        </button>
      ))}
    </div>
  );
}

// ---------- context menu ----------

export type MenuItem =
  | { kind: "sep" }
  | { kind: "label"; text: string }
  | {
      kind: "item";
      label: string;
      hint?: string;
      icon?: ReactNode;
      danger?: boolean;
      disabled?: boolean;
      onSelect: () => void;
    };

export interface MenuState {
  x: number;
  y: number;
  header?: ReactNode;
  items: MenuItem[];
}

export function ContextMenu({ state, onClose }: { state: MenuState; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x: state.x, y: state.y });

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    let x = state.x;
    let y = state.y;
    if (x + r.width > window.innerWidth - 8) x = Math.max(8, window.innerWidth - r.width - 8);
    if (y + r.height > window.innerHeight - 8) y = Math.max(8, window.innerHeight - r.height - 8);
    setPos({ x, y });
  }, [state.x, state.y]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) onClose();
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("mousedown", onDown, true);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("mousedown", onDown, true);
    };
  }, [onClose]);

  return (
    <div className="menu" ref={ref} style={{ left: pos.x, top: pos.y }} onContextMenu={(e) => e.preventDefault()}>
      {state.header && <div className="head">{state.header}</div>}
      {state.items.map((it, i) => {
        if (it.kind === "sep") return <div className="sep" key={i} />;
        if (it.kind === "label") return <div className="sub" key={i}>{it.text}</div>;
        return (
          <button
            key={i}
            className={clsx(it.danger && "danger")}
            disabled={it.disabled}
            onClick={() => {
              onClose();
              it.onSelect();
            }}
          >
            {it.icon}
            <span>{it.label}</span>
            {it.hint && <span className="k">{it.hint}</span>}
          </button>
        );
      })}
    </div>
  );
}

// ---------- toasts ----------

export interface Toast {
  id: number;
  ok: boolean;
  title: string;
  detail?: string;
}

export function Toasts({ items, onDone }: { items: Toast[]; onDone: (id: number) => void }) {
  const done = useRef(onDone);
  done.current = onDone;
  useEffect(() => {
    if (!items.length) return;
    const id = items[0]!.id;
    const t = setTimeout(() => done.current(id), 4600);
    return () => clearTimeout(t);
  }, [items]);
  return (
    <div className="toasts">
      {items.map((t) => (
        <div key={t.id} className={clsx("toast", t.ok ? "ok" : "err")}>
          {t.ok ? <Check /> : <AlertTriangle />}
          <div className="body">
            <b>{t.title}</b>
            {t.detail && <span>{t.detail}</span>}
          </div>
        </div>
      ))}
    </div>
  );
}

// ---------- modal ----------

export function Modal({
  title,
  children,
  footer,
  onClose,
  width,
}: {
  title: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  onClose: () => void;
  width?: number;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal-card" style={width ? { width } : undefined}>
        <div className="modal-head">
          <h3>{title}</h3>
          <button className="btn icon ghost sm x" onClick={onClose}>
            <X size={15} />
          </button>
        </div>
        <div className="modal-body">{children}</div>
        {footer && <div className="modal-foot">{footer}</div>}
      </div>
    </div>
  );
}

// ---------- process picker ----------

export function ProcessPicker({
  onPick,
  onClose,
}: {
  onPick: (name: string, path: string) => void;
  onClose: () => void;
}) {
  const [q, setQ] = useState("");
  const [list, setList] = useState<ProcInfo[] | null>(null);
  const [icons, setIcons] = useState<Record<string, string>>({});
  const [all, setAll] = useState(false);

  useEffect(() => {
    let alive = true;
    api
      .processes()
      .then((p) => alive && setList(p))
      .catch(() => alive && setList([]));
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    if (!list) return;
    let alive = true;
    const missing = list.slice(0, 120).filter((p) => !(p.path in icons));
    (async () => {
      for (const p of missing) {
        try {
          const url = await api.icon(p.path);
          if (!alive) return;
          setIcons((m) => ({ ...m, [p.path]: url }));
        } catch {
          /* icon is optional */
        }
      }
    })();
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list]);

  const filtered = useMemo(() => {
    if (!list) return [];
    const terms = q.toLowerCase().split(/\s+/).filter(Boolean);
    return list.filter((p) => {
      if (!all && p.system) return false;
      const hay = `${p.name} ${p.path} ${p.pid}`.toLowerCase();
      return terms.every((t) => hay.includes(t));
    });
  }, [list, q, all]);

  return (
    <Modal
      title="选择正在运行的应用"
      onClose={onClose}
      width={620}
      footer={
        <>
          <label className="check">
            <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} />
            显示系统进程
          </label>
          <span className="right tip">{filtered.length} 个结果</span>
        </>
      }
    >
      <div className="search" style={{ margin: "2px 4px 10px" }}>
        <Search />
        <input
          className="input"
          style={{ width: "100%" }}
          autoFocus
          placeholder="按名称、路径或 PID 搜索"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
      </div>
      {!list && (
        <div className="empty">
          <Loader2 className="spin" />
          <b>正在读取进程列表</b>
        </div>
      )}
      {list && filtered.length === 0 && (
        <div className="empty">
          <Search />
          <b>没有匹配的应用</b>
        </div>
      )}
      {filtered.slice(0, 300).map((p) => (
        <button key={p.path} className="proc-row" onClick={() => onPick(p.name, p.path)}>
          <span className="ico">
            {icons[p.path] ? <img src={icons[p.path]} alt="" /> : <ShieldCheck size={13} />}
          </span>
          <span style={{ minWidth: 0 }}>
            <div className="nm">{p.name}</div>
            <div className="pth">{p.path}</div>
          </span>
          <span className="meta">
            {p.instances > 1 ? `${p.instances} 个实例 · ` : ""}
            PID {p.pid}
          </span>
        </button>
      ))}
    </Modal>
  );
}

// ---------- rule editor ----------

export function RuleEditor({
  initial,
  proxies,
  insertAt,
  onSave,
  onClose,
}: {
  initial?: Rule;
  proxies: Proxy[];
  insertAt: number;
  onSave: (r: Rule, insertAt: number) => Promise<void>;
  onClose: () => void;
}) {
  const [r, setR] = useState<Rule>(
    initial ?? {
      key: "",
      process: "",
      hosts: "*",
      ports: "*",
      domains: "",
      protocol: 0,
      action: 0,
      proxyKey: proxies[0]?.key ?? "",
      localhost: false,
      enabled: true,
    },
  );
  const [pick, setPick] = useState(false);
  const [busy, setBusy] = useState(false);

  const set = <K extends keyof Rule>(k: K, v: Rule[K]) => setR((x) => ({ ...x, [k]: v }));
  const preview = describeRule(r, proxies);

  const save = async () => {
    setBusy(true);
    try {
      await onSave(r, initial ? 0 : insertAt);
      onClose();
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <div className="scrim" onClick={onClose} />
      <aside className="panel">
        <div className="panel-head">
          <h2>{initial ? "编辑规则" : "新建规则"}</h2>
          <span className="chip muted">#{initial ? "编辑" : insertAt || "末尾"}</span>
          <button className="btn icon ghost sm x" onClick={onClose}>
            <X size={15} />
          </button>
        </div>
        <div className="panel-body">
          <Field label="进程" hint="可写多个：chrome.exe,msedge.exe；* 表示所有应用">
            <div className="field-row">
              <input
                className="input mono"
                value={r.process}
                placeholder="chrome.exe"
                onChange={(e) => set("process", e.target.value)}
              />
              <button className="btn" onClick={() => setPick(true)}>
                <Activity size={14} /> 选择应用
              </button>
            </div>
          </Field>

          <div className="field-row">
            <Field label="域名" hint="支持 *.example.com，分号分隔">
              <input
                className="input mono"
                value={r.domains}
                placeholder="*.github.com"
                onChange={(e) => set("domains", e.target.value)}
              />
            </Field>
          </div>
          <div className="field-row">
            <Field label="目标地址 / IP">
              <input
                className="input mono"
                value={r.hosts}
                placeholder="*"
                onChange={(e) => set("hosts", e.target.value)}
              />
            </Field>
            <Field label="端口">
              <input
                className="input mono"
                value={r.ports}
                placeholder="* 或 80;443;8000-9000"
                onChange={(e) => set("ports", e.target.value)}
              />
            </Field>
          </div>

          <Field label="协议">
            <Seg<Protocol>
              value={r.protocol}
              onChange={(v) => set("protocol", v)}
              options={[
                { value: 0, label: "TCP" },
                { value: 1, label: "UDP" },
                { value: 2, label: "两者" },
              ]}
            />
          </Field>

          <Field label="动作">
            <Seg<RuleAction>
              value={r.action}
              onChange={(v) => set("action", v)}
              options={[
                { value: 0, label: "走代理", dot: "var(--accent)" },
                { value: 1, label: "直连", dot: "var(--mint)" },
                { value: 2, label: "阻断", dot: "var(--coral)" },
              ]}
            />
          </Field>

          {r.action === 0 && (
            <Field label="使用代理" hint={proxies.length ? undefined : "还没有代理配置"}>
              <select
                className="input"
                value={r.proxyKey}
                onChange={(e) => set("proxyKey", e.target.value)}
              >
                {proxies.map((p) => (
                  <option key={p.key} value={p.key}>
                    {p.name} · {PROXY_NAMES[p.type]} {p.ip}:{p.port}
                  </option>
                ))}
                <option value="">（自动：第一个代理）</option>
              </select>
            </Field>
          )}

          <div className="checks">
            <label className="check">
              <input type="checkbox" checked={r.localhost} onChange={(e) => set("localhost", e.target.checked)} />
              同时代理 localhost
            </label>
            <label className="check">
              <input type="checkbox" checked={r.enabled} onChange={(e) => set("enabled", e.target.checked)} />
              启用
            </label>
          </div>

          <div className="preview">
            {preview.split("→")[0]}
            <b> → {preview.split("→").slice(1).join("→").trim()}</b>
          </div>
          {initial && <div className="tip">提示：规则自上而下匹配，第一条命中的规则生效。</div>}
        </div>
        <div className="panel-foot">
          <button className="btn" onClick={onClose}>
            取消
          </button>
          <button className="btn primary right" onClick={save} disabled={busy || !r.process.trim()}>
            {busy ? <Loader2 className="spin" size={14} /> : <Check size={14} />}
            保存规则
          </button>
        </div>
      </aside>
      {pick && (
        <ProcessPicker
          onClose={() => setPick(false)}
          onPick={(name) => {
            set("process", name);
            setPick(false);
          }}
        />
      )}
    </>
  );
}

// ---------- proxy editor ----------

export function ProxyEditor({
  initial,
  onSave,
  onClose,
}: {
  initial?: Proxy;
  onSave: (p: Proxy) => Promise<void>;
  onClose: () => void;
}) {
  const [p, setP] = useState<Proxy>(
    initial ?? { key: "", name: "", type: 1, ip: "127.0.0.1", port: 1080, sendDomain: true },
  );
  const [busy, setBusy] = useState(false);
  const set = <K extends keyof Proxy>(k: K, v: Proxy[K]) => setP((x) => ({ ...x, [k]: v }));

  return (
    <>
      <div className="scrim" onClick={onClose} />
      <aside className="panel" style={{ width: 440 }}>
        <div className="panel-head">
          <h2>{initial ? "编辑代理" : "新增代理"}</h2>
          <button className="btn icon ghost sm x" onClick={onClose}>
            <X size={15} />
          </button>
        </div>
        <div className="panel-body">
          <Field label="名称">
            <input
              className="input"
              value={p.name}
              placeholder="香港 SOCKS5"
              onChange={(e) => set("name", e.target.value)}
            />
          </Field>
          <Field label="类型">
            <Seg<0 | 1>
              value={p.type}
              onChange={(v) => set("type", v)}
              options={[
                { value: 1, label: "SOCKS5" },
                { value: 0, label: "HTTP" },
              ]}
            />
          </Field>
          <div className="field-row">
            <Field label="地址">
              <input className="input mono" value={p.ip} onChange={(e) => set("ip", e.target.value)} />
            </Field>
            <div style={{ width: 110 }}>
              <Field label="端口">
                <input
                  className="input mono"
                  value={p.port || ""}
                  onChange={(e) => set("port", Number(e.target.value) || 0)}
                />
              </Field>
            </div>
          </div>
          <div className="field-row">
            <Field label="用户名（可选）">
              <input className="input mono" value={p.user ?? ""} onChange={(e) => set("user", e.target.value)} />
            </Field>
            <Field label="密码（可选）">
              <input
                className="input mono"
                type="password"
                value={p.pass ?? ""}
                onChange={(e) => set("pass", e.target.value)}
              />
            </Field>
          </div>
          <label className="check">
            <input type="checkbox" checked={p.sendDomain} onChange={(e) => set("sendDomain", e.target.checked)} />
            交给代理解析域名（远程 DNS）
          </label>
          <div className="tip">
            UDP 只在 SOCKS5 下生效；HTTP 代理会让 UDP 自动回退为直连。
          </div>
        </div>
        <div className="panel-foot">
          <button className="btn" onClick={onClose}>
            取消
          </button>
          <button
            className="btn primary right"
            disabled={busy || !p.ip || !p.port}
            onClick={async () => {
              setBusy(true);
              try {
                await onSave(p);
                onClose();
              } finally {
                setBusy(false);
              }
            }}
          >
            {busy ? <Loader2 className="spin" size={14} /> : <Check size={14} />}
            保存
          </button>
        </div>
      </aside>
    </>
  );
}

// ---------- confirm ----------

export function Confirm({
  title,
  body,
  confirmLabel = "删除",
  onConfirm,
  onClose,
}: {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  onConfirm: () => void;
  onClose: () => void;
}) {
  return (
    <Modal
      title={title}
      onClose={onClose}
      width={420}
      footer={
        <>
          <button className="btn" onClick={onClose}>
            取消
          </button>
          <button
            className="btn danger right"
            onClick={() => {
              onConfirm();
              onClose();
            }}
          >
            {confirmLabel}
          </button>
        </>
      }
    >
      <div style={{ padding: "4px 6px 8px", color: "var(--dim)", fontSize: 12.5 }}>{body}</div>
    </Modal>
  );
}
