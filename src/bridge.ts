// Typed client for the Go side. The page cannot import the generated
// src/mygo.ts wrappers because the view model is hand-written here, so this
// calls the runtime directly with the same method names.
import { call } from "mygo-runtime";

export type ProxyType = 0 | 1; // HTTP | SOCKS5
export type RuleAction = 0 | 1 | 2; // PROXY | DIRECT | BLOCK
export type Protocol = 0 | 1 | 2; // TCP | UDP | BOTH

export interface Proxy {
  key: string;
  name: string;
  type: ProxyType;
  ip: string;
  port: number;
  user?: string;
  pass?: string;
  sendDomain: boolean;
}

export interface Rule {
  key: string;
  process: string;
  hosts: string;
  ports: string;
  domains: string;
  protocol: Protocol;
  action: RuleAction;
  proxyKey?: string;
  localhost: boolean;
  enabled: boolean;
  note?: string;
}

export interface Flow {
  id: number;
  pid: number;
  proxyId: number;
  ruleId: number;
  ageMs: number;
  sent: number;
  received: number;
  port: number;
  udp: boolean;
  ipv6: boolean;
  name: string;
  host: string;
  ruleKey?: string;
  proxyKey?: string;
  proxyName?: string;
}

export type EventKind = "conn" | "log" | "test";

export interface LogEvent {
  seq: number;
  time: string;
  kind: EventKind;
  process?: string;
  pid?: number;
  host?: string;
  port?: number;
  info?: string;
  text?: string;
  ruleKey?: string;
  proxyKey?: string;
}

export interface TestRun {
  proxyKey: string;
  target: string;
  lines: string[];
  running: boolean;
  result?: number;
  started: string;
}

export interface Stats {
  events: number;
  flows: number;
  up: number;
  down: number;
  replays: number;
}

export interface View {
  mode: "offline" | "service";
  connected: boolean;
  version: number;
  error?: string;
  enabled: boolean;
  trafficLog: boolean;
  logAll: boolean;
  proxies: Proxy[];
  rules: Rule[];
  flows: Flow[];
  events: LogEvent[];
  seq: number;
  test?: TestRun;
  stats: Stats;
  started: string;
  ui: UIState;
}

export interface ServiceStatus {
  installed: boolean;
  running: boolean;
  startType: string;
  binPath: string;
  error?: string;
}

export interface AppStatus {
  service: ServiceStatus;
  admin: boolean;
  dataDir: string;
  settingsPath: string;
  engineDir: string;
  engineReady: boolean;
  autoStart: boolean;
  version: string;
  platform: string;
}

export interface RuleTarget {
  flowId?: number;
  process: string;
  pid: number;
  host: string;
  port: number;
  udp: boolean;
  proxyKey?: string;
  matchedRuleKey?: string;
}

export interface RuleOpts {
  scope: "host" | "process" | "process-host";
  action: RuleAction;
  proxyKey: string;
  ports: string;
  localhost: boolean;
  moveToTop: boolean;
}

export interface RuleResult {
  flowId: number;
  ruleKey: string;
  process: string;
  created: boolean;
  merged: boolean;
  position: number;
  summary: string;
}

export interface ProcInfo {
  pid: number;
  name: string;
  path: string;
  instances: number;
  system: boolean;
}

export interface UIState {
  page: string;
  theme: "light" | "dark";
  showLogs: boolean;
  compact: boolean;
}

const get = <T>(method: string, ...args: unknown[]): Promise<T> =>
  call<T>(`API.${method}`, ...args);

export const api = {
  view: (since: number) => get<View>("View", since),
  connect: () => get<void>("Connect"),
  disconnect: () => get<void>("Disconnect"),
  setEnabled: (on: boolean) => get<void>("SetEnabled", on),
  setTrafficLog: (on: boolean) => get<void>("SetTrafficLog", on),
  setLogAll: (on: boolean) => get<void>("SetLogAll", on),
  clearEvents: () => get<View>("ClearEvents"),
  saveProxy: (p: Proxy) => get<Proxy>("SaveProxy", p),
  deleteProxy: (key: string) => get<void>("DeleteProxy", key),
  testProxy: (key: string) => get<void>("TestProxy", key),
  saveRule: (r: Rule, insertAt: number) => get<Rule>("SaveRule", r, insertAt),
  deleteRule: (key: string) => get<void>("DeleteRule", key),
  toggleRule: (key: string, on: boolean) => get<void>("ToggleRule", key, on),
  reorderRules: (keys: string[]) => get<void>("ReorderRules", keys),
  rulesFromTargets: (targets: RuleTarget[], opts: RuleOpts) =>
    get<RuleResult[]>("RulesFromTargets", targets, opts),
  processes: () => get<ProcInfo[]>("Processes"),
  icon: (path: string) => get<string>("Icon", path),
  status: () => get<AppStatus>("Status"),
  installEngine: () => get<void>("InstallEngine"),
  uninstallEngine: () => get<void>("UninstallEngine"),
  saveUI: (ui: UIState) => get<void>("SaveUI", ui),
  setAutoStart: (on: boolean) => get<void>("SetAutoStart", on),
  revealData: () => get<void>("RevealData"),
  revealEngine: () => get<void>("RevealEngine"),
};

// ---- helpers ----

export const PROXY_NAMES: Record<ProxyType, string> = { 0: "HTTP", 1: "SOCKS5" };
export const ACTION_NAMES: Record<RuleAction, string> = { 0: "走代理", 1: "直连", 2: "阻断" };
export const PROTO_NAMES: Record<Protocol, string> = { 0: "TCP", 1: "UDP", 2: "TCP+UDP" };

export function fmtBytes(n: number): string {
  if (!n) return "0";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v >= 100 ? Math.round(v) : v.toFixed(v >= 10 ? 0 : 1)}${units[i]}`;
}

export function fmtAge(ms: number): string {
  if (ms === 0xffffffff || ms < 0) return "—";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

export function fmtTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "--:--:--";
  return d.toLocaleTimeString("zh-CN", { hour12: false });
}

export function isIP(host: string): boolean {
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(host) || (host.includes(":") && /^[0-9a-f:]+$/i.test(host));
}

export function describeRule(r: Rule, proxies: Proxy[]): string {
  const action =
    r.action === 2 ? "阻断" : r.action === 1 ? "直连" : `走 ${proxies.find((p) => p.key === r.proxyKey)?.name ?? "第一个代理"}`;
  const where = r.domains || (r.hosts && r.hosts !== "*" ? r.hosts : "所有主机");
  const port = r.ports && r.ports !== "*" ? `:${r.ports}` : "";
  return `${r.process || "*"} → ${where}${port} ${action}`;
}

export function matchChipClass(action: RuleAction): string {
  return action === 0 ? "proxy" : action === 1 ? "direct" : "block";
}

// eventAction reads the engine's connection info line: "Proxy ... (UDP)",
// "Direct", "Blocked". Everything else is treated as proxied.
export function eventAction(e: LogEvent): RuleAction {
  const info = (e.info ?? "").toLowerCase();
  if (info.includes("block")) return 2;
  if (info.includes("direct")) return 1;
  return 0;
}

// displayHost prefers a domain seen in a flow for the same process and port,
// because connection events only carry the destination IP.
export function displayHost(e: LogEvent, flows: Flow[]): string {
  const host = e.host ?? "";
  if (!host || !isIP(host)) return host;
  const hit = flows.find(
    (f) => f.pid === e.pid && f.port === e.port && f.host && !isIP(f.host),
  );
  return hit?.host ?? host;
}
