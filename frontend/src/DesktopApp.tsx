import { Call, Events, System, Window } from "@wailsio/runtime";
import { Check, CircleAlert, Download, ExternalLink, HardDrive, LoaderCircle, LogOut, Minus, Package, RefreshCw, RotateCcw, Settings, Square, Unplug, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { BrandMark } from "./BrandMark";
import { DesktopPageHeader } from "./DesktopPageHeader";
import { AppShell, AuthLayout, Badge, Button, EmptyState, IconButton, InlineNotice, Input, Loading, PageHeader, PasswordInput, Progress, ScrollArea, errorMessage } from "./components/ui";
import type { DeviceView } from "./types";

const service = "github.com/wangshangbin/homestack/internal/desktop.Service";

interface Session { logged_in: boolean; control_url?: string; expires_at?: string }
interface Provider { id: string; label: string }
interface TailnetStatus { online: boolean; tailnet_ip?: string; connection: string; error?: string }
interface UpdateStatus {
  state: "idle" | "checking" | "up-to-date" | "available" | "downloading" | "verifying" | "ready" | "error";
  current_version: string; latest_version?: string; published_at?: string; notes?: string;
  downloaded?: number; total?: number; signature: string; mode: string; error?: string; skipped_version?: string;
}
export interface ManagedComponentStatus {
  id: string; label: string; version?: string; phase: string; downloaded?: number; total?: number;
  speed_bps?: number; source_host?: string; error?: string;
}
export interface ManagedContentStatus {
  state: "idle" | "preparing" | "ready" | "error" | "cancelled";
  phase: string; downloaded?: number; total?: number; speed_bps?: number; error?: string;
  components: ManagedComponentStatus[];
}

const desktopNav = [
  { to: "/", label: "设备", icon: HardDrive, end: true },
  { to: "/updates", label: "应用更新", icon: Download },
  { to: "/settings", label: "设置", icon: Settings },
];

export function DesktopApp() {
  const [session, setSession] = useState<Session | null>(null);
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const [tailnet, setTailnet] = useState<TailnetStatus | null>(null);
  const [updateAvailable, setUpdateAvailable] = useState(false);
  const [managed, setManaged] = useState<ManagedContentStatus | null>(null);
  const [error, setError] = useState("");
  const startupTriggered = useRef(false);
  const navigate = useNavigate();

  const refresh = useCallback(async (): Promise<Session | null> => {
    setError("");
    let current: Session;
    try {
      current = await Call.ByName(`${service}.Session`) as Session;
      setSession(current);
    } catch (reason) {
      setError(errorMessage(reason));
      return null;
    }
    const requests: Array<Promise<unknown>> = [
      Call.ByName(`${service}.ManagedContentStatus`),
      Call.ByName(`${service}.LocalStatus`),
      ...(current.logged_in ? [Call.ByName(`${service}.Devices`)] : []),
    ];
    const [managedResult, tailnetResult, devicesResult] = await Promise.allSettled(requests);
    const failures: string[] = [];
    if (managedResult.status === "fulfilled") setManaged(managedResult.value as ManagedContentStatus); else failures.push(errorMessage(managedResult.reason));
    if (tailnetResult.status === "fulfilled") setTailnet(tailnetResult.value as TailnetStatus); else failures.push(errorMessage(tailnetResult.reason));
    if (current.logged_in && devicesResult) {
      if (devicesResult.status === "fulfilled") setDevices(devicesResult.value as DeviceView[]); else failures.push(errorMessage(devicesResult.reason));
    }
    if (failures.length > 0) setError(failures.join("；"));
    return current;
  }, []);

  const ensureManaged = useCallback(async () => {
    try { setManaged(await Call.ByName(`${service}.EnsureManagedContentPreparation`) as ManagedContentStatus); }
    catch (reason) { setError(errorMessage(reason)); }
  }, []);

  useEffect(() => {
    const off = Events.On("homestack:managed-content-progress", (event) => setManaged(event.data as ManagedContentStatus));
    void refresh().then((current) => {
      if (current?.logged_in && !startupTriggered.current) { startupTriggered.current = true; void ensureManaged(); }
    });
    return off;
  }, [ensureManaged, refresh]);

  useEffect(() => {
    const timer = window.setInterval(() => void refresh(), 10_000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void Call.ByName(`${service}.CheckForUpdates`).then((value) => setUpdateAvailable((value as UpdateStatus).state === "available"))
        .catch(() => Call.ByName(`${service}.UpdateStatus`).then((value) => setUpdateAvailable((value as UpdateStatus).state === "available")));
    }, 1000);
    return () => window.clearTimeout(timer);
  }, []);

  async function logout() {
    try { await Call.ByName(`${service}.Logout`); setSession({ logged_in: false }); setDevices([]); setManaged(null); navigate("/"); }
    catch (reason) { setError(errorMessage(reason)); }
  }

  async function resumeManaged() {
    setError("");
    try { await Call.ByName(`${service}.ResumeManagedContentPreparation`); setManaged(await Call.ByName(`${service}.ManagedContentStatus`) as ManagedContentStatus); }
    catch (reason) { setError(errorMessage(reason)); }
  }

  async function cancelManaged() {
    setError("");
    try { await Call.ByName(`${service}.CancelManagedContentPreparation`); }
    catch (reason) { setError(errorMessage(reason)); }
  }

  async function completeLogin() {
    const current = await refresh();
    if (current?.logged_in) { startupTriggered.current = true; await ensureManaged(); navigate("/"); }
  }

  if (session === null && error) return <AuthLayout title="无法启动 HomeStack" description="读取本机核心身份失败。"><InlineNotice tone="danger">{error}</InlineNotice><Button onClick={() => void refresh()}><RefreshCw size={16} />重试</Button></AuthLayout>;
  if (session === null) return <Loading label="正在读取 HomeStack 身份" />;
  if (!session.logged_in) return <div className="desktop-shell"><DesktopChrome /><LoginPanel onLogin={completeLogin} /></div>;

  const shellActions = <><IconButton label="刷新" onClick={() => void refresh()}><RefreshCw size={16} /></IconButton><IconButton label="退出登录" onClick={() => void logout()}><LogOut size={16} /></IconButton></>;
  return <div className="desktop-shell"><DesktopChrome /><div className="desktop-surface"><AppShell actions={shellActions} nav={desktopNav} product={updateAvailable ? "Desktop · 有更新" : "Desktop"}><div className="desktop-content"><ManagedContentNotice onCancel={cancelManaged} onResume={resumeManaged} status={managed} /><Routes><Route path="/" element={<DeviceList devices={devices} tailnet={tailnet} setError={setError} />} /><Route path="/updates" element={<UpdatesPage onAvailabilityChange={setUpdateAvailable} />} /><Route path="/settings" element={<SettingsPage session={session} tailnet={tailnet} managed={managed} onPrepare={resumeManaged} />} /><Route path="*" element={<Navigate to="/" replace />} /></Routes>{error && <InlineNotice tone="danger">{error}</InlineNotice>}</div></AppShell></div></div>;
}

function DesktopChrome() {
  return <header className={`desktop-chrome window-drag ${System.IsMac() ? "mac" : ""}`}><div className="chrome-brand"><BrandMark className="brand-mark" /><strong>HomeStack</strong></div>{!System.IsMac() && <div className="window-no-drag window-buttons"><button onClick={() => void Window.Minimise()} title="最小化"><Minus size={15} /></button><button onClick={() => void Window.ToggleMaximise()} title="最大化或还原"><Square size={13} /></button><button className="close" onClick={() => void Window.Close()} title="关闭"><X size={16} /></button></div>}</header>;
}

function LoginPanel({ onLogin }: { onLogin: () => Promise<void> }) {
  const [controlURL, setControlURL] = useState("");
  const [providers, setProviders] = useState<Provider[]>([]);
  const [activationCode, setActivationCode] = useState("");
  const [operation, setOperation] = useState<"idle" | "discovering" | "authenticating">("idle");
  const [error, setError] = useState("");
  async function discover() { setOperation("discovering"); setError(""); try { setProviders(await Call.ByName(`${service}.Providers`, controlURL.trim()) as Provider[]); } catch (reason) { setError(errorMessage(reason)); } finally { setOperation("idle"); } }
  async function login(provider: string) { setOperation("authenticating"); setError(""); try { await Call.ByName(`${service}.Login`, controlURL.trim(), provider); await onLogin(); } catch (reason) { setError(errorMessage(reason)); } finally { setOperation("idle"); } }
  async function activate() { setOperation("authenticating"); setError(""); try { await Call.ByName(`${service}.Activate`, controlURL.trim(), activationCode.trim()); await onLogin(); } catch (reason) { setError(errorMessage(reason)); } finally { setOperation("idle"); } }
  const busy = operation !== "idle";
  return <AuthLayout title="连接 HomeStack" description="身份验证与设备登记完成后即可进入，组件会在后台准备。"><div className="login-controls"><Input aria-label="Control 地址" type="url" value={controlURL} onChange={(event) => setControlURL(event.target.value)} placeholder="https://home.example.com" /><Button disabled={busy || !controlURL} loading={operation === "discovering"} onClick={() => void discover()}>读取登录方式</Button></div>{providers.length > 0 && <div className="provider-list">{providers.map((provider) => <Button disabled={busy} key={provider.id} loading={operation === "authenticating"} onClick={() => void login(provider.id)} tone="default" variant="outline">{provider.label}</Button>)}</div>}<div className="login-controls"><PasswordInput aria-label="一次性激活码" value={activationCode} onChange={(event) => setActivationCode(event.target.value)} placeholder="一次性激活码" /><Button disabled={busy || !controlURL || !activationCode} loading={operation === "authenticating"} onClick={() => void activate()} tone="default" variant="outline">使用激活码</Button></div>{error && <InlineNotice tone="danger">{error}</InlineNotice>}</AuthLayout>;
}

function ManagedContentNotice({ onCancel, onResume, status }: { onCancel: () => Promise<void>; onResume: () => Promise<void>; status: ManagedContentStatus | null }) {
  if (!status || status.state === "idle" || status.state === "ready") return null;
  if (status.state === "preparing") return <InlineNotice action={<Button onClick={() => void onCancel()} size="sm" tone="default" variant="outline">取消</Button>} title="设备已激活，本机能力准备中" tone="info">{managedPhaseLabel(status.phase)}。文件与影视能力就绪后会自动更新。</InlineNotice>;
  return <InlineNotice action={<Button onClick={() => void onResume()} size="sm"><RefreshCw size={14} />重新准备</Button>} title={status.state === "cancelled" ? "本机能力准备已取消" : "本机能力准备失败"} tone={status.state === "cancelled" ? "warning" : "danger"}>{status.error || "当前登录和设备登记仍然有效，可手动重试。"}</InlineNotice>;
}

export function ManagedContentPreparation({ status, onCancel, onResume, error }: { status: ManagedContentStatus; onCancel: () => Promise<void>; onResume: () => Promise<void>; error?: string }) {
  const total = status.total || 0; const downloaded = Math.min(status.downloaded || 0, total || Number.MAX_SAFE_INTEGER);
  const percent = total > 0 ? Math.round((downloaded / total) * 100) : 0;
  const remaining = status.speed_bps && total > downloaded ? Math.ceil((total - downloaded) / status.speed_bps) : 0;
  const active = status.state === "preparing"; const indeterminate = isIndeterminateManagedPhase(status.phase);
  return <main className="managed-preparation"><header className="managed-heading"><BrandMark className="login-mark" /><div><h1>准备文件与影视</h1><p>{managedPhaseLabel(status.phase)}</p></div></header><div className="managed-overall"><div><strong>{total > 0 ? `${percent}%` : managedPhaseLabel(status.phase)}</strong><span>{total > 0 ? `${formatBytes(downloaded)} / ${formatBytes(total)}` : "正在读取组件信息"}</span></div><div><span>{status.speed_bps ? `${formatBytes(status.speed_bps)}/s` : "-"}</span><span>{remaining ? `约 ${formatDuration(remaining)}` : ""}</span></div></div><Progress className="managed-progress" indeterminate={indeterminate} label="组件准备总进度" value={percent} /><div className="managed-component-list">{status.components.map((component) => <ManagedComponentRow key={component.id} component={component} />)}</div><div className="button-row">{active ? <Button onClick={() => void onCancel()} tone="default" variant="outline"><X size={16} />取消</Button> : <Button onClick={() => void onResume()}><RefreshCw size={16} />继续准备</Button>}</div>{(error || status.error) && <InlineNotice tone="danger">{error || status.error}</InlineNotice>}</main>;
}

function ManagedComponentRow({ component }: { component: ManagedComponentStatus }) {
  const percent = component.total ? Math.round(((component.downloaded || 0) / component.total) * 100) : 0;
  const failed = component.phase === "error"; const ready = component.phase === "ready"; const indeterminate = isIndeterminateManagedPhase(component.phase);
  return <div className={`managed-component ${failed ? "failed" : ""}`}><span className="managed-component-icon">{ready ? <Check size={17} /> : failed ? <CircleAlert size={17} /> : component.phase === "pending" ? <Package size={17} /> : <LoaderCircle className="spin" size={17} />}</span><div className="managed-component-copy"><strong>{component.label}</strong><small>{component.version || managedPhaseLabel(component.phase)}{component.source_host ? ` · ${component.source_host}` : ""}</small>{component.error && <small className="managed-error">{component.error}</small>}</div><div className="managed-component-state"><strong>{managedPhaseLabel(component.phase)}</strong>{component.phase === "downloading" && <small>{formatBytes(component.downloaded || 0)} / {formatBytes(component.total || 0)}</small>}</div><Progress indeterminate={indeterminate} value={ready ? 100 : percent} /></div>;
}

function DeviceList({ devices, tailnet, setError }: { devices: DeviceView[]; tailnet: TailnetStatus | null; setError: (value: string) => void }) {
  async function open(id: string) { setError(""); try { await Call.ByName(`${service}.OpenDevice`, id); } catch (reason) { setError(errorMessage(reason)); } }
  const contentState = (device: DeviceView) => { const content = (device.status?.capabilities || []).filter((item) => item.id === "files" || item.id === "media"); if (content.length < 2 || content.some((item) => item.state === "disabled")) return { label: "准备中", tone: "warning" as const, detail: "" }; if (content.every((item) => item.state === "ready")) return { label: "内容已就绪", tone: "success" as const, detail: "" }; return { label: "内容错误", tone: "danger" as const, detail: content.find((item) => item.state !== "ready")?.detail || "文件或影视服务未就绪" }; };
  return <section className="device-page"><PageHeader description={tailnet?.online ? `${tailnet.connection} · ${tailnet.tailnet_ip || "Tailnet"}` : "请先登录 Tailscale"} title="设备" />{devices.length === 0 ? <EmptyState icon={<Unplug size={24} />} title="尚未登记设备" /> : <div className="device-list">{devices.map((device) => { const content = contentState(device); return <article className="device-row" key={device.id}><span className="device-icon"><HardDrive size={19} /></span><div className="device-copy"><strong>{device.name}</strong><small>{content.detail || device.status?.tailscale_ip || device.magic_dns || device.agent_url}</small></div><Badge tone={device.status?.online ? "success" : "neutral"}>{device.status?.online ? "在线" : "离线"}</Badge><Badge tone={content.tone}>{content.label}</Badge><IconButton label={`打开 ${device.name}`} onClick={() => void open(device.id)}><ExternalLink size={17} /></IconButton></article>; })}</div>}</section>;
}

function UpdatesPage({ onAvailabilityChange }: { onAvailabilityChange: (value: boolean) => void }) {
  const [status, setStatus] = useState<UpdateStatus | null>(null); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const navigate = useNavigate();
  const applyStatus = useCallback((value: UpdateStatus) => { setStatus(value); onAvailabilityChange(value.state === "available"); }, [onAvailabilityChange]);
  const load = useCallback(async () => applyStatus(await Call.ByName(`${service}.UpdateStatus`) as UpdateStatus), [applyStatus]);
  const check = useCallback(async () => { setBusy(true); setError(""); try { applyStatus(await Call.ByName(`${service}.CheckForUpdates`) as UpdateStatus); } catch (reason) { setError(errorMessage(reason)); await load(); } finally { setBusy(false); } }, [applyStatus, load]);
  useEffect(() => { void load(); }, [load]);
  async function download() { setBusy(true); setError(""); const operation = Call.ByName(`${service}.DownloadUpdate`) as Promise<UpdateStatus>; const timer = window.setInterval(() => void load().catch((reason) => setError(errorMessage(reason))), 250); try { applyStatus(await operation); } catch (reason) { setError(errorMessage(reason)); await load(); } finally { window.clearInterval(timer); setBusy(false); } }
  async function skip() { if (!status?.latest_version) return; setBusy(true); setError(""); try { await Call.ByName(`${service}.SkipUpdate`, status.latest_version); await load(); } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); } }
  if (!status) return <section className="compact-page"><DesktopPageHeader title="应用更新" /><Loading label="读取更新状态" compact /></section>;
  const percent = status.total ? Math.round(((status.downloaded || 0) / status.total) * 100) : 0;
  return <section className="compact-page"><DesktopPageHeader title="应用更新" /><div className="update-grid"><span>当前版本</span><strong>{status.current_version}</strong><span>最新版本</span><strong>{status.latest_version || "-"}</strong><span>状态</span><strong>{updateLabel(status.state)}</strong><span>签名</span><strong>{status.signature}</strong></div>{status.published_at && <p className="muted">发布于 {new Date(status.published_at).toLocaleString("zh-CN")}</p>}{status.notes && <ScrollArea className="release-notes">{status.notes}</ScrollArea>}{status.state === "downloading" && <Progress label="应用更新下载进度" value={percent} />}<div className="button-row"><Button disabled={busy} onClick={() => void check()} tone="default" variant="outline"><RefreshCw size={16} />检查更新</Button>{status.state === "available" && <Button disabled={busy} onClick={() => void skip()} tone="default" variant="outline">忽略此版本</Button>}{status.state === "available" && (status.mode === "deb" ? <Button onClick={() => void Call.ByName(`${service}.OpenUpdateRelease`)}><ExternalLink size={16} />前往下载</Button> : <Button disabled={busy} loading={busy} onClick={() => void download()}><Download size={16} />下载并安装</Button>)}{status.state === "ready" && <Button onClick={() => navigate("/")} tone="default" variant="outline">稍后重启</Button>}{status.state === "ready" && <Button onClick={() => void Call.ByName(`${service}.RestartForUpdate`)}><RotateCcw size={16} />立即重启</Button>}</div>{(error || status.error) && <InlineNotice tone="danger">{error || status.error}</InlineNotice>}</section>;
}

function SettingsPage({ session, tailnet, managed, onPrepare }: { session: Session; tailnet: TailnetStatus | null; managed: ManagedContentStatus | null; onPrepare: () => Promise<void> }) {
  const sourceLabel = (component: ManagedComponentStatus) => component.id === "node" ? "本机" : component.source_host || (component.phase === "ready" ? "历史安装" : "尚未下载");
  return <section className="compact-page"><DesktopPageHeader title="设置" /><div className="settings-list"><div><span>Control</span><strong>{session.control_url || "未登录"}</strong></div><div><span>Tailnet</span><strong>{tailnet?.online ? tailnet.tailnet_ip : "未连接"}</strong></div><div><span>数据通道</span><strong>官方 Tailscale 设备直连</strong></div></div><div className="settings-section-heading"><h2>托管组件</h2><Button disabled={managed?.state === "preparing"} onClick={() => void onPrepare()} size="sm" tone="default" variant="outline"><RefreshCw size={15} />重新准备</Button></div><div className="managed-settings-list">{managed?.components.map((component) => <div key={component.id}><span><strong>{component.label}</strong><small>{component.version || "-"}</small></span><span>{sourceLabel(component)}</span><Badge tone={component.phase === "ready" ? "success" : component.phase === "error" ? "danger" : "neutral"}>{managedPhaseLabel(component.phase)}</Badge></div>)}</div></section>;
}

function updateLabel(state: UpdateStatus["state"]) { return ({ idle: "空闲", checking: "检查中", "up-to-date": "已是最新", available: "有更新", downloading: "下载中", verifying: "签名校验中", ready: "待重启", error: "错误" })[state]; }
function managedPhaseLabel(phase: string) { return ({ idle: "等待准备", manifest: "读取清单", pending: "等待", selecting: "测速选源", downloading: "下载中", verifying: "校验中", extracting: "解压中", installing: "安装中", saving: "保存配置", configuring: "配置服务", starting: "启动 Node", health: "健康检查", ready: "已就绪", error: "失败", cancelled: "已取消" } as Record<string, string>)[phase] || phase; }
function isIndeterminateManagedPhase(phase: string) { return ["manifest", "selecting", "verifying", "extracting", "installing", "saving", "configuring", "starting", "health"].includes(phase); }
function formatBytes(value: number) { if (!Number.isFinite(value) || value <= 0) return "0 B"; const units = ["B", "KiB", "MiB", "GiB"]; const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1); return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`; }
function formatDuration(seconds: number) { if (seconds < 60) return `${seconds} 秒`; const minutes = Math.ceil(seconds / 60); return minutes < 60 ? `${minutes} 分钟` : `${Math.ceil(minutes / 60)} 小时`; }
