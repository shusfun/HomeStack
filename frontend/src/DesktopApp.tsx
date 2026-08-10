import { Call, System, Window } from "@wailsio/runtime";
import { Download, ExternalLink, HardDrive, LogOut, Minus, RefreshCw, RotateCcw, Settings, Square, Unplug, X } from "lucide-react";
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

export function DesktopApp() {
  const [session, setSession] = useState<Session | null>(null);
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const [tailnet, setTailnet] = useState<TailnetStatus | null>(null);
  const [updateAvailable, setUpdateAvailable] = useState(false);
  const [error, setError] = useState("");
  const navigate = useNavigate();

  const refresh = useCallback(async () => {
    setError("");
    try {
      const current = await Call.ByName(`${service}.Session`) as Session;
      setSession(current);
      setTailnet(await Call.ByName(`${service}.LocalStatus`) as TailnetStatus);
      if (current.logged_in) setDevices(await Call.ByName(`${service}.Devices`) as DeviceView[]);
    } catch (reason) { setError(errorMessage(reason)); }
  }, []);
  useEffect(() => { void refresh(); }, [refresh]);
  useEffect(() => { const timer = window.setInterval(() => void refresh(), 10_000); return () => window.clearInterval(timer); }, [refresh]);
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

  if (session === null && error) return <section className="login-panel"><BrandMark className="login-mark" /><h1>文件与影视准备失败</h1><Feedback error={error} /><button className="primary-button" onClick={() => void refresh()}><RefreshCw size={16} />重试</button></section>;
  if (session === null) return <CenteredLoader label="正在准备文件与影视" />;
  return <div className="desktop-shell">
    <DesktopChrome onRefresh={refresh} loggedIn={session.logged_in} onLogout={logout} updateAvailable={updateAvailable} />
    <main className="desktop-content">
      <Routes>
        <Route path="/" element={session.logged_in ? <DeviceList devices={devices} tailnet={tailnet} setError={setError} /> : <LoginPanel onLogin={refresh} />} />
        <Route path="/updates" element={<UpdatesPage onAvailabilityChange={setUpdateAvailable} />} />
        <Route path="/settings" element={<SettingsPage session={session} tailnet={tailnet} />} />
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

function SettingsPage({ session, tailnet }: { session: Session; tailnet: TailnetStatus | null }) {
  return <section className="compact-page"><DesktopPageHeader title="设置" /><div className="settings-list"><div><span>Control</span><strong>{session.control_url || "未登录"}</strong></div><div><span>Tailnet</span><strong>{tailnet?.online ? tailnet.tailnet_ip : "未连接"}</strong></div><div><span>数据通道</span><strong>官方 Tailscale 设备直连</strong></div></div></section>;
}

function updateLabel(state: UpdateStatus["state"]) { return ({ idle: "空闲", checking: "检查中", "up-to-date": "已是最新", available: "有更新", downloading: "下载中", verifying: "签名校验中", ready: "待重启", error: "错误" })[state]; }
