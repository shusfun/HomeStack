import { Call, Events, System, Window } from "@wailsio/runtime";
import { Check, CircleAlert, Download, ExternalLink, HardDrive, LoaderCircle, LogOut, Minus, Package, RefreshCw, RotateCcw, Settings, Square, Unplug, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { NavLink, Route, Routes, useNavigate } from "react-router-dom";
import { BrandMark } from "./BrandMark";
import { DesktopPageHeader } from "./DesktopPageHeader";
import type { DeviceView } from "./types";
import { CenteredLoader, EmptyState, Feedback, StatusPill, connectionTone, errorMessage } from "./ui";

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

export function DesktopApp() {
  const [session, setSession] = useState<Session | null>(null);
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const [tailnet, setTailnet] = useState<TailnetStatus | null>(null);
  const [updateAvailable, setUpdateAvailable] = useState(false);
  const [managed, setManaged] = useState<ManagedContentStatus | null>(null);
  const [error, setError] = useState("");
  const navigate = useNavigate();

  const refresh = useCallback(async () => {
    setError("");
    try {
      setManaged(await Call.ByName(`${service}.ManagedContentStatus`) as ManagedContentStatus);
      const current = await Call.ByName(`${service}.Session`) as Session;
      setSession(current);
      setTailnet(await Call.ByName(`${service}.LocalStatus`) as TailnetStatus);
      if (current.logged_in) setDevices(await Call.ByName(`${service}.Devices`) as DeviceView[]);
    } catch (reason) { setError(errorMessage(reason)); }
  }, []);
  useEffect(() => {
    const off = Events.On("homestack:managed-content-progress", (event) => setManaged(event.data as ManagedContentStatus));
    void refresh();
    return off;
  }, [refresh]);
  const managedActive = managed?.state === "preparing";
  useEffect(() => {
    if (managedActive) return;
    const timer = window.setInterval(() => void refresh(), 10_000);
    return () => window.clearInterval(timer);
  }, [managedActive, refresh]);
  useEffect(() => {
    const timer = window.setTimeout(() => {
      void Call.ByName(`${service}.CheckForUpdates`).then((value) => {
        setUpdateAvailable((value as UpdateStatus).state === "available");
      }).catch(() => {
        void Call.ByName(`${service}.UpdateStatus`).then((value) => {
          setUpdateAvailable((value as UpdateStatus).state === "available");
        });
      });
    }, 1000);
    return () => window.clearTimeout(timer);
  }, []);

  async function logout() {
    await Call.ByName(`${service}.Logout`); setSession({ logged_in: false }); setDevices([]); navigate("/");
  }

  async function resumeManaged() {
    setError("");
    try { await Call.ByName(`${service}.ResumeManagedContentPreparation`); await refresh(); }
    catch (reason) { setError(errorMessage(reason)); }
  }

  async function cancelManaged() {
    setError("");
    try { await Call.ByName(`${service}.CancelManagedContentPreparation`); }
    catch (reason) { setError(errorMessage(reason)); }
  }

  if (managed && (managed.state === "preparing" || managed.state === "error" || managed.state === "cancelled")) return <ManagedContentPreparation status={managed} onCancel={cancelManaged} onResume={resumeManaged} error={error} />;
  if (session === null && error) return <section className="login-panel"><BrandMark className="login-mark" /><h1>文件与影视准备失败</h1><Feedback error={error} /><button className="primary-button" onClick={() => void refresh()}><RefreshCw size={16} />重试</button></section>;
  if (session === null) return <CenteredLoader label="正在准备文件与影视" />;
  return <div className="desktop-shell">
    <DesktopChrome onRefresh={refresh} loggedIn={session.logged_in} onLogout={logout} updateAvailable={updateAvailable} />
    <main className="desktop-content">
      <Routes>
        <Route path="/" element={session.logged_in ? <DeviceList devices={devices} tailnet={tailnet} setError={setError} /> : <LoginPanel onLogin={refresh} />} />
        <Route path="/updates" element={<UpdatesPage onAvailabilityChange={setUpdateAvailable} />} />
        <Route path="/settings" element={<SettingsPage session={session} tailnet={tailnet} managed={managed} onPrepare={resumeManaged} />} />
        <Route path="*" element={<DeviceList devices={devices} tailnet={tailnet} setError={setError} />} />
      </Routes>
      <Feedback error={error} />
    </main>
  </div>;
}

function DesktopChrome({ onRefresh, loggedIn, onLogout, updateAvailable }: { onRefresh: () => Promise<void>; loggedIn: boolean; onLogout: () => Promise<void>; updateAvailable: boolean }) {
  return <header className={`desktop-chrome window-drag ${System.IsMac() ? "mac" : ""}`}>
    <div className="chrome-brand"><BrandMark className="brand-mark" /><strong>HomeStack</strong></div>
    <nav className="window-no-drag chrome-actions">
      <button className="icon-button" onClick={() => void onRefresh()} title="刷新" aria-label="刷新"><RefreshCw size={16} /></button>
      <NavLink className={`icon-button ${updateAvailable ? "has-update" : ""}`} to="/updates" title="应用更新" aria-label="应用更新"><Download size={16} /></NavLink>
      <NavLink className="icon-button" to="/settings" title="设置" aria-label="设置"><Settings size={16} /></NavLink>
      {loggedIn && <button className="icon-button" onClick={() => void onLogout()} title="退出登录" aria-label="退出登录"><LogOut size={16} /></button>}
      {!System.IsMac() && <div className="window-buttons"><button onClick={() => void Window.Minimise()} title="最小化"><Minus size={15} /></button><button onClick={() => void Window.ToggleMaximise()} title="最大化或还原"><Square size={13} /></button><button className="close" onClick={() => void Window.Close()} title="关闭"><X size={16} /></button></div>}
    </nav>
  </header>;
}

function LoginPanel({ onLogin }: { onLogin: () => Promise<void> }) {
  const [controlURL, setControlURL] = useState(""); const [providers, setProviders] = useState<Provider[]>([]);
  const [activationCode, setActivationCode] = useState("");
  const [operation, setOperation] = useState<"idle" | "discovering" | "preparing">("idle"); const [error, setError] = useState("");
  async function discover() {
    setOperation("discovering"); setError("");
    try { setProviders(await Call.ByName(`${service}.Providers`, controlURL.trim()) as Provider[]); }
    catch (reason) { setError(errorMessage(reason)); } finally { setOperation("idle"); }
  }
  async function login(provider: string) {
    setOperation("preparing"); setError("");
    try { await Call.ByName(`${service}.Login`, controlURL.trim(), provider); await onLogin(); }
    catch (reason) { setError(errorMessage(reason)); } finally { setOperation("idle"); }
  }
  async function activate() {
    setOperation("preparing"); setError("");
    try { await Call.ByName(`${service}.Activate`, controlURL.trim(), activationCode.trim()); await onLogin(); }
    catch (reason) { setError(errorMessage(reason)); } finally { setOperation("idle"); }
  }
  const busy = operation !== "idle";
  return <section className="login-panel"><BrandMark className="login-mark" /><h1>连接 HomeStack</h1><div className="login-controls"><input type="url" value={controlURL} onChange={(event) => setControlURL(event.target.value)} placeholder="https://home.example.com" /><button className="primary-button" disabled={busy || !controlURL} onClick={() => void discover()}>{operation === "discovering" ? "正在读取登录方式" : "读取登录方式"}</button></div>{providers.length > 0 && <div className="provider-list">{providers.map((provider) => <button key={provider.id} disabled={busy} onClick={() => void login(provider.id)}>{operation === "preparing" ? "正在准备文件与影视" : provider.label}</button>)}</div>}<div className="login-controls"><input type="password" value={activationCode} onChange={(event) => setActivationCode(event.target.value)} placeholder="一次性激活码" /><button className="secondary-button" disabled={busy || !controlURL || !activationCode} onClick={() => void activate()}>{operation === "preparing" ? "正在准备文件与影视" : "使用激活码"}</button></div><Feedback error={error} /></section>;
}

export function ManagedContentPreparation({ status, onCancel, onResume, error }: { status: ManagedContentStatus; onCancel: () => Promise<void>; onResume: () => Promise<void>; error?: string }) {
  const total = status.total || 0; const downloaded = Math.min(status.downloaded || 0, total || Number.MAX_SAFE_INTEGER);
  const percent = total > 0 ? Math.round((downloaded / total) * 100) : 0;
  const remaining = status.speed_bps && total > downloaded ? Math.ceil((total - downloaded) / status.speed_bps) : 0;
  const active = status.state === "preparing";
  const indeterminate = isIndeterminateManagedPhase(status.phase);
  return <main className="managed-preparation">
    <header className="managed-heading"><BrandMark className="login-mark" /><div><h1>准备文件与影视</h1><p>{managedPhaseLabel(status.phase)}</p></div></header>
    <div className="managed-overall"><div><strong>{total > 0 ? `${percent}%` : managedPhaseLabel(status.phase)}</strong><span>{total > 0 ? `${formatBytes(downloaded)} / ${formatBytes(total)}` : "正在读取组件信息"}</span></div><div><span>{status.speed_bps ? `${formatBytes(status.speed_bps)}/s` : "-"}</span><span>{remaining ? `约 ${formatDuration(remaining)}` : ""}</span></div></div>
    <div className={`progress managed-progress ${indeterminate ? "indeterminate" : ""}`} aria-label="组件准备总进度"><span style={{ width: indeterminate ? undefined : `${percent}%` }} /></div>
    <div className="managed-component-list">{status.components.map((component) => <ManagedComponentRow key={component.id} component={component} />)}</div>
    <div className="button-row">{active ? <button className="secondary-button" onClick={() => void onCancel()}><X size={16} />取消</button> : <button className="primary-button" onClick={() => void onResume()}><RefreshCw size={16} />继续准备</button>}</div>
    <Feedback error={error || status.error} />
  </main>;
}

function ManagedComponentRow({ component }: { component: ManagedComponentStatus }) {
  const percent = component.total ? Math.round(((component.downloaded || 0) / component.total) * 100) : 0;
  const failed = component.phase === "error";
  const ready = component.phase === "ready";
  const indeterminate = isIndeterminateManagedPhase(component.phase);
  return <div className={`managed-component ${failed ? "failed" : ""}`}>
    <span className="managed-component-icon">{ready ? <Check size={17} /> : failed ? <CircleAlert size={17} /> : component.phase === "pending" ? <Package size={17} /> : <LoaderCircle className="spin" size={17} />}</span>
    <div className="managed-component-copy"><strong>{component.label}</strong><small>{component.version || managedPhaseLabel(component.phase)}{component.source_host ? ` · ${component.source_host}` : ""}</small>{component.error && <small className="managed-error">{component.error}</small>}</div>
    <div className="managed-component-state"><strong>{managedPhaseLabel(component.phase)}</strong>{component.phase === "downloading" && <small>{formatBytes(component.downloaded || 0)} / {formatBytes(component.total || 0)}</small>}</div>
    <div className={`progress ${indeterminate ? "indeterminate" : ""}`}><span style={{ width: indeterminate ? undefined : `${ready ? 100 : percent}%` }} /></div>
  </div>;
}

function DeviceList({ devices, tailnet, setError }: { devices: DeviceView[]; tailnet: TailnetStatus | null; setError: (value: string) => void }) {
  async function open(id: string) { setError(""); try { await Call.ByName(`${service}.OpenDevice`, id); } catch (reason) { setError(errorMessage(reason)); } }
  const contentState = (device: DeviceView) => { const content = (device.status?.capabilities || []).filter((item) => item.id === "files" || item.id === "media"); if (content.length < 2 || content.some((item) => item.state === "disabled")) return { label: "准备中", tone: "warn" as const, detail: "" }; if (content.every((item) => item.state === "ready")) return { label: "内容已就绪", tone: "good" as const, detail: "" }; return { label: "内容错误", tone: "warn" as const, detail: content.find((item) => item.state !== "ready")?.detail || "文件或影视服务未就绪" }; };
  return <section className="device-page"><div className="summary-line"><div><h1>设备</h1><p>{tailnet?.online ? `${tailnet.connection} · ${tailnet.tailnet_ip || "Tailnet"}` : "请先登录 Tailscale"}</p></div></div>{devices.length === 0 ? <EmptyState icon={<Unplug size={24} />} label="尚未登记设备" /> : <div className="device-list">{devices.map((device) => { const content = contentState(device); return <article className="device-row" key={device.id}><span className="device-icon"><HardDrive size={19} /></span><div className="device-copy"><strong>{device.name}</strong><small>{content.detail || device.status?.tailscale_ip || device.magic_dns || device.agent_url}</small></div><StatusPill label={device.status?.online ? "在线" : "离线"} tone={device.status?.online ? "good" : "muted"} /><StatusPill label={content.label} tone={content.tone} /><button className="icon-button" onClick={() => void open(device.id)} title="用浏览器打开" aria-label={`打开 ${device.name}`}><ExternalLink size={17} /></button></article>; })}</div>}</section>;
}

function UpdatesPage({ onAvailabilityChange }: { onAvailabilityChange: (value: boolean) => void }) {
  const [status, setStatus] = useState<UpdateStatus | null>(null); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const navigate = useNavigate();
  const applyStatus = useCallback((value: UpdateStatus) => { setStatus(value); onAvailabilityChange(value.state === "available"); }, [onAvailabilityChange]);
  const load = useCallback(async () => { applyStatus(await Call.ByName(`${service}.UpdateStatus`) as UpdateStatus); }, [applyStatus]);
  const check = useCallback(async () => { setBusy(true); setError(""); try { applyStatus(await Call.ByName(`${service}.CheckForUpdates`) as UpdateStatus); } catch (reason) { setError(errorMessage(reason)); await load(); } finally { setBusy(false); } }, [applyStatus, load]);
  useEffect(() => { void load(); }, [load]);
  async function download() {
    setBusy(true); setError("");
    const operation = Call.ByName(`${service}.DownloadUpdate`) as Promise<UpdateStatus>;
    const timer = window.setInterval(() => { void load().catch((reason) => setError(errorMessage(reason))); }, 250);
    try { applyStatus(await operation); } catch (reason) { setError(errorMessage(reason)); await load(); }
    finally { window.clearInterval(timer); setBusy(false); }
  }
  async function skip() { if (!status?.latest_version) return; setBusy(true); setError(""); try { await Call.ByName(`${service}.SkipUpdate`, status.latest_version); await load(); } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); } }
  if (!status) return <section className="compact-page"><DesktopPageHeader title="应用更新" /><CenteredLoader label="读取更新状态" compact /></section>;
  const percent = status.total ? Math.round(((status.downloaded || 0) / status.total) * 100) : 0;
  return <section className="compact-page"><DesktopPageHeader title="应用更新" /><div className="update-grid"><span>当前版本</span><strong>{status.current_version}</strong><span>最新版本</span><strong>{status.latest_version || "-"}</strong><span>状态</span><strong>{updateLabel(status.state)}</strong><span>签名</span><strong>{status.signature}</strong></div>{status.published_at && <p className="muted">发布于 {new Date(status.published_at).toLocaleString("zh-CN")}</p>}{status.notes && <pre className="release-notes">{status.notes}</pre>}{status.state === "downloading" && <div className="progress"><span style={{ width: `${percent}%` }} /></div>}<div className="button-row"><button className="secondary-button" disabled={busy} onClick={() => void check()}><RefreshCw size={16} />检查更新</button>{status.state === "available" && <button className="secondary-button" disabled={busy} onClick={() => void skip()}>忽略此版本</button>}{status.state === "available" && (status.mode === "deb" ? <button className="primary-button" onClick={() => void Call.ByName(`${service}.OpenUpdateRelease`)}><ExternalLink size={16} />前往下载</button> : <button className="primary-button" disabled={busy} onClick={() => void download()}><Download size={16} />下载并安装</button>)}{status.state === "ready" && <button className="secondary-button" onClick={() => navigate("/")}>稍后重启</button>}{status.state === "ready" && <button className="primary-button" onClick={() => void Call.ByName(`${service}.RestartForUpdate`)}><RotateCcw size={16} />立即重启</button>}</div><Feedback error={error || status.error} /></section>;
}

function SettingsPage({ session, tailnet, managed, onPrepare }: { session: Session; tailnet: TailnetStatus | null; managed: ManagedContentStatus | null; onPrepare: () => Promise<void> }) {
  const sourceLabel = (component: ManagedComponentStatus) => component.id === "node" ? "本机" : component.source_host || (component.phase === "ready" ? "历史安装" : "尚未下载");
  return <section className="compact-page"><DesktopPageHeader title="设置" /><div className="settings-list"><div><span>Control</span><strong>{session.control_url || "未登录"}</strong></div><div><span>Tailnet</span><strong>{tailnet?.online ? tailnet.tailnet_ip : "未连接"}</strong></div><div><span>数据通道</span><strong>官方 Tailscale 设备直连</strong></div></div><div className="settings-section-heading"><h2>托管组件</h2><button className="secondary-button" disabled={managed?.state === "preparing"} onClick={() => void onPrepare()}><RefreshCw size={15} />重新准备</button></div><div className="managed-settings-list">{managed?.components.map((component) => <div key={component.id}><span><strong>{component.label}</strong><small>{component.version || "-"}</small></span><span>{sourceLabel(component)}</span><StatusPill label={managedPhaseLabel(component.phase)} tone={component.phase === "ready" ? "good" : component.phase === "error" ? "warn" : "muted"} /></div>)}</div></section>;
}

function updateLabel(state: UpdateStatus["state"]) { return ({ idle: "空闲", checking: "检查中", "up-to-date": "已是最新", available: "有更新", downloading: "下载中", verifying: "签名校验中", ready: "待重启", error: "错误" })[state]; }

function managedPhaseLabel(phase: string) { return ({ idle: "等待准备", manifest: "读取清单", pending: "等待", selecting: "测速选源", downloading: "下载中", verifying: "校验中", extracting: "解压中", installing: "安装中", saving: "保存配置", configuring: "配置服务", starting: "启动 Node", health: "健康检查", ready: "已就绪", error: "失败", cancelled: "已取消" } as Record<string, string>)[phase] || phase; }
function isIndeterminateManagedPhase(phase: string) { return ["manifest", "selecting", "verifying", "extracting", "installing", "saving", "configuring", "starting", "health"].includes(phase); }
function formatBytes(value: number) { if (!Number.isFinite(value) || value <= 0) return "0 B"; const units = ["B", "KiB", "MiB", "GiB"]; const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1); return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`; }
function formatDuration(seconds: number) { if (seconds < 60) return `${seconds} 秒`; const minutes = Math.ceil(seconds / 60); return minutes < 60 ? `${minutes} 分钟` : `${Math.ceil(minutes / 60)} 小时`; }
