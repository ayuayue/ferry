import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  api,
  isIP,
  type AppStatus,
  type LogEvent,
  type Proxy,
  type Rule,
  type RuleTarget,
  type View,
} from "./bridge";
import {
  ContextMenu,
  ProxyEditor,
  Rail,
  TitleBar,
  Toasts,
  type MenuState,
  type Page,
  type Toast,
} from "./components";
import { ConnectionsPage, ProxiesPage, RuleDrawer, RulesPage, SettingsPage } from "./pages";

type Notify = (ok: boolean, title: string, detail?: string) => void;

const EMPTY: View = {
  mode: "offline",
  connected: false,
  version: 0,
  error: "",
  enabled: false,
  trafficLog: false,
  logAll: false,
  proxies: [],
  rules: [],
  flows: [],
  events: [],
  seq: 0,
  stats: { events: 0, flows: 0, up: 0, down: 0, replays: 0 },
  started: "",
};

export default function App() {
  const [view, setView] = useState<View>(EMPTY);
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [page, setPage] = useState<Page>("connections");
  const [logOpen, setLogOpen] = useState(false);
  const [events, setEvents] = useState<LogEvent[]>([]);
  const [selected, setSelected] = useState<number[]>([]);
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [drawer, setDrawer] = useState<{ open: boolean; rule: Rule | null; at: number }>({
    open: false,
    rule: null,
    at: 1,
  });
  const [proxyEdit, setProxyEdit] = useState<{ open: boolean; proxy: Proxy | null }>({
    open: false,
    proxy: null,
  });

  const since = useRef(0);
  const seq = useRef(0);
  const fails = useRef(0);
  const busy = useRef(false);

  const toast = useCallback<Notify>((ok, title, detail) => {
    seq.current += 1;
    setToasts((cur) => [...cur, { id: seq.current, ok, title, detail }].slice(-4));
  }, []);

  const refreshStatus = useCallback(async () => {
    try {
      setStatus(await api.status());
    } catch {
      /* the settings page shows what is missing */
    }
  }, []);

  const tick = useCallback(async () => {
    if (busy.current) return;
    busy.current = true;
    try {
      const v = await api.view(since.current);
      since.current = Math.max(since.current, v.seq);
      fails.current = 0;
      if (v.events.length) {
        setEvents((cur) => [...v.events.slice().reverse(), ...cur].slice(0, 800));
      }
      setView(v);
    } catch (e) {
      fails.current += 1;
      if (fails.current === 4) toast(false, "与 Go 后端通信中断", String(e));
    } finally {
      busy.current = false;
    }
  }, [toast]);

  useEffect(() => {
    let stopped = false;
    (async () => {
      await refreshStatus();
      try {
        await api.connect(false);
      } catch {
        try {
          await api.connect(true);
          toast(true, "已连接到演示引擎", "未检测到引擎服务，当前是假数据");
        } catch (e) {
          toast(false, "引擎未连接", String(e));
        }
      }
      if (!stopped) await tick();
      if (!stopped) await refreshStatus();
    })();
    const t = setInterval(() => {
      if (!stopped) void tick();
    }, 900);
    return () => {
      stopped = true;
      clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openCustomRule = useCallback(
    (targets: RuleTarget[]) => {
      const t = targets[0];
      if (!t) return;
      const host = t.host || "";
      const rule: Rule = {
        key: "",
        process: t.process || "*",
        hosts: host && isIP(host) ? host : "*",
        ports: t.port ? String(t.port) : "*",
        domains: host && !isIP(host) ? host : "",
        protocol: t.udp ? 1 : 0,
        action: 0,
        proxyKey: t.proxyKey || view.proxies[0]?.key || "",
        localhost: false,
        enabled: true,
      };
      setDrawer({ open: true, rule, at: 1 });
    },
    [view.proxies],
  );

  const quickRule = useCallback(
    async (targets: RuleTarget[], mode: "quick" | "custom" = "quick") => {
      if (!targets.length) return;
      if (mode === "custom") {
        openCustomRule(targets);
        return;
      }
      const first = targets[0]!;
      try {
        const res = await api.rulesFromTargets(targets, {
          scope: "host",
          action: 0,
          proxyKey: first.proxyKey || "",
          ports: "*",
          localhost: false,
          moveToTop: false,
        });
        const created = res.filter((r) => r.created).length;
        const merged = res.length - created;
        const pos = res.map((r) => r.position).filter((p) => p > 0);
        toast(
          true,
          created
            ? `已添加 ${created} 条规则${merged ? `，合并 ${merged} 条` : ""}`
            : `已合并 ${merged} 条规则`,
          `${res[0]?.summary ?? ""}${pos.length ? ` · 位于第 ${pos[0]} 条` : ""}`,
        );
        void tick();
      } catch (e) {
        toast(false, "生成规则失败", String(e));
      }
    },
    [openCustomRule, tick, toast],
  );

  const ruleCount = useMemo(() => view.rules.filter((r) => r.enabled).length, [view.rules]);

  return (
    <div className="shell">
      <TitleBar view={view} onStatus={() => setPage("settings")} />
      <div className="body">
        <Rail
          page={page}
          view={view}
          onPage={(p) => {
            setPage(p);
            setSelected([]);
          }}
        />
        <main className="pb-main">
          {page === "connections" && (
            <ConnectionsPage
              view={view}
              events={events}
              selected={selected}
              setSelected={setSelected}
              logOpen={logOpen}
              onLogToggle={setLogOpen}
              onMenu={setMenu}
              onToast={toast}
              onNewRule={quickRule}
            />
          )}
          {page === "rules" && (
            <RulesPage
              view={view}
              onToast={toast}
              onEdit={(rule, at) => setDrawer({ open: true, rule, at })}
            />
          )}
          {page === "proxies" && (
            <ProxiesPage
              view={view}
              onToast={toast}
              onEdit={(proxy) => setProxyEdit({ open: true, proxy })}
            />
          )}
          {page === "settings" && (
            <SettingsPage
              view={view}
              status={status}
              onStatus={() => {
                void refreshStatus();
                void tick();
              }}
              onToast={toast}
            />
          )}
        </main>
      </div>

      {drawer.open && (
        <RuleDrawer
          open={drawer.open}
          initial={drawer.rule}
          insertAt={drawer.at}
          proxies={view.proxies}
          onClose={() => setDrawer({ open: false, rule: null, at: 1 })}
          onToast={(ok, title, detail) => {
            toast(ok, title, detail);
            if (ok) void tick();
          }}
        />
      )}

      {proxyEdit.open && (
        <ProxyEditor
          initial={proxyEdit.proxy ?? undefined}
          onClose={() => setProxyEdit({ open: false, proxy: null })}
          onSave={async (p) => {
            try {
              await api.saveProxy(p);
              toast(true, proxyEdit.proxy ? "代理已更新" : "代理已创建", `${p.ip}:${p.port}`);
              void tick();
            } catch (e) {
              toast(false, "保存失败", String(e));
              throw e;
            }
          }}
        />
      )}

      {menu && <ContextMenu state={menu} onClose={() => setMenu(null)} />}
      <Toasts items={toasts} onDone={(id) => setToasts((cur) => cur.filter((t) => t.id !== id))} />

      <div className="statusbar">
        <span>规则 {ruleCount}/{view.rules.length}</span>
        <span>连接 {view.flows.length}</span>
        <span>日志 {events.length}</span>
        <span className="right mono">
          {view.connected ? `${view.mode} · proto v${view.version}` : "offline"}
        </span>
      </div>
    </div>
  );
}
