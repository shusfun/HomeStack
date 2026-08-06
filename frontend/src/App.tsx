import { Call } from "@wailsio/runtime";
import {
  Check,
  ChevronLeft,
  CircleAlert,
  Clipboard,
  ExternalLink,
  File,
  Film,
  Folder,
  FolderOpen,
  HardDrive,
  KeyRound,
  LoaderCircle,
  LogIn,
  LogOut,
  MonitorCog,
  Network,
  Plus,
  RefreshCw,
  Server,
  ShieldCheck,
  Unplug,
} from "lucide-react";
import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { NavLink, Route, Routes, useNavigate } from "react-router-dom";
import { api, detectSurface, formatBytes, formatTime } from "./api";
import type { DeviceStatus, DeviceView, FileItem, FileResource, MediaItem, Surface } from "./types";

interface Me {
  subject: string;
  email: string;
  name: string;
  admin: boolean;
}

interface LocalStatus {
  configured: boolean;
  device_id?: string;
  device_name?: string;
  online: boolean;
  tailnet_ip?: string;
  connection: string;
  error?: string;
}

const desktopService = "github.com/wangshangbin/homestack/internal/desktop.Service";

export default function App() {
  const [surface, setSurface] = useState<Surface | null>(null);
  useEffect(() => {
    void detectSurface().then(setSurface);
  }, []);
  if (!surface) return <CenteredLoader label="正在连接" />;
  if (surface === "desktop") return <DesktopApp />;
  if (surface === "control") return <ControlApp />;
  return <AgentApp />;
}

function DesktopApp() {
  const [connection, setConnection] = useState("");
  const [status, setStatus] = useState<LocalStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  const refresh = useCallback(async () => {
    try {
      const result = (await Call.ByName(`${desktopService}.LocalStatus`)) as LocalStatus;
      setStatus(result);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function join(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = (await Call.ByName(`${desktopService}.Join`, connection.trim())) as { message: string };
      setMessage(result.message);
      setConnection("");
      await refresh();
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="desktop-shell">
      <Topbar surface="桌面端" />
      <main className="desktop-main">
        <section className="join-panel" aria-labelledby="join-title">
          <div className="section-heading">
            <div>
              <span className="eyebrow">安全连接</span>
              <h1 id="join-title">连接 HomeStack</h1>
            </div>
            <ShieldCheck size={28} aria-hidden="true" />
          </div>
          <form onSubmit={join} className="join-form">
            <label htmlFor="connection">连接信息</label>
            <textarea
              id="connection"
              value={connection}
              onChange={(event) => setConnection(event.target.value)}
              placeholder="homestack://join?server=https://...&code=..."
              rows={4}
              spellCheck={false}
              required
            />
            <button className="primary-button" disabled={busy || !connection.trim()}>
              {busy ? <LoaderCircle className="spin" size={18} /> : <KeyRound size={18} />}
              {busy ? "等待 Passkey" : "连接"}
            </button>
          </form>
          <Feedback message={message} error={error} />
        </section>
        <section className="status-strip" aria-label="本机状态">
          <StatusMetric icon={<HardDrive size={18} />} label="设备" value={status?.device_name || "未配置"} />
          <StatusMetric icon={<Network size={18} />} label="连接" value={status?.connection || "检测中"} tone={connectionTone(status?.connection)} />
          <StatusMetric icon={<Server size={18} />} label="尾网地址" value={status?.tailnet_ip || "-"} />
          <button className="icon-button" onClick={() => void refresh()} title="刷新状态" aria-label="刷新状态">
            <RefreshCw size={18} />
          </button>
        </section>
        {status?.error && <InlineError message={status.error} />}
      </main>
    </div>
  );
}

function ControlApp() {
  const [me, setMe] = useState<Me | null>(null);
  const [authChecked, setAuthChecked] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    api<Me>("/api/v1/me")
      .then(setMe)
      .catch(() => setMe(null))
      .finally(() => setAuthChecked(true));
  }, []);

  if (!authChecked) return <CenteredLoader label="验证身份" />;
  if (!me) {
    return (
      <div className="login-shell">
        <div className="login-mark"><ShieldCheck size={30} /></div>
        <h1>HomeStack</h1>
        <a className="primary-button" href="/auth/login?return=/">
          <LogIn size={18} /> 使用 Passkey 登录
        </a>
        <Feedback error={error} />
      </div>
    );
  }

  async function logout() {
    try {
      await api<void>("/auth/logout", { method: "POST" });
      location.assign("/");
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }

  return (
    <AppFrame
      surface="控制台"
      navigation={[
        { to: "/", label: "设备", icon: <HardDrive size={18} /> },
        ...(me.admin ? [{ to: "/connect", label: "连接", icon: <Plus size={18} /> }] : []),
      ]}
      footer={
        <button className="nav-button" onClick={() => void logout()}>
          <LogOut size={17} /> 退出
        </button>
      }
    >
      <Routes>
        <Route path="/" element={<DevicesPage me={me} />} />
        <Route path="/connect" element={me.admin ? <InvitePage /> : <DevicesPage me={me} />} />
        <Route path="*" element={<DevicesPage me={me} />} />
      </Routes>
      <Feedback error={error} />
    </AppFrame>
  );
}

function DevicesPage({ me }: { me: Me }) {
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const response = await api<{ devices: DeviceView[] }>("/api/v1/devices");
      setDevices(response.devices);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => void load(), [load]);

  async function openDevice(device: DeviceView) {
    try {
      const ticket = await api<{ url: string }>(`/api/v1/devices/${encodeURIComponent(device.id)}/tickets`, { method: "POST" });
      location.assign(ticket.url);
    } catch (reason) {
      setError(errorMessage(reason));
    }
  }

  return (
    <Page title="设备" action={<button className="icon-button" onClick={() => void load()} title="刷新设备" aria-label="刷新设备"><RefreshCw size={18} /></button>}>
      <div className="identity-line">{me.name || me.email}</div>
      {loading ? <CenteredLoader label="读取设备" compact /> : devices.length === 0 ? <EmptyState icon={<Unplug size={24} />} label="暂无设备" /> : (
        <div className="device-list">
          {devices.map((device) => (
            <article className="device-card" key={device.id}>
              <div className="device-main">
                <div className="device-icon"><HardDrive size={21} /></div>
                <div>
                  <h2>{device.name}</h2>
                  <p>{device.status?.tailnet_ip || device.agent_url}</p>
                </div>
              </div>
              <div className="device-facts">
                <StatusPill label={device.status?.online ? "在线" : "离线"} tone={device.status?.online ? "good" : "muted"} />
                <StatusPill label={device.status?.connection || "未连接"} tone={connectionTone(device.status?.connection)} />
                <span>{formatTime(device.status?.last_seen)}</span>
              </div>
              <button className="icon-button" onClick={() => void openDevice(device)} title="打开设备" aria-label={`打开 ${device.name}`}>
                <ExternalLink size={18} />
              </button>
            </article>
          ))}
        </div>
      )}
      <Feedback error={error} />
    </Page>
  );
}

function InvitePage() {
  const [deviceName, setDeviceName] = useState("");
  const [agentURL, setAgentURL] = useState("");
  const [filebrowser, setFilebrowser] = useState(true);
  const [filebrowserURL, setFilebrowserURL] = useState("http://127.0.0.1:8080");
  const [filebrowserToken, setFilebrowserToken] = useState("");
  const [jellyfin, setJellyfin] = useState(true);
  const [jellyfinURL, setJellyfinURL] = useState("http://127.0.0.1:8096");
  const [jellyfinKey, setJellyfinKey] = useState("");
  const [ccEnabled, setCCEnabled] = useState(false);
  const [projectName, setProjectName] = useState("");
  const [workDir, setWorkDir] = useState("");
  const [botID, setBotID] = useState("");
  const [botSecret, setBotSecret] = useState("");
  const [allowFrom, setAllowFrom] = useState("");
  const [adminFrom, setAdminFrom] = useState("");
  const [joinInfo, setJoinInfo] = useState("");
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");

  async function createInvite(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError("");
    setJoinInfo("");
    const modules = [];
    const moduleSecrets: Record<string, Record<string, string>> = {};
    if (filebrowser) {
      modules.push({ id: "filebrowser", enabled: true, base_url: filebrowserURL, read_only: true });
      moduleSecrets.filebrowser = { api_token: filebrowserToken };
    }
    if (jellyfin) {
      modules.push({ id: "jellyfin", enabled: true, base_url: jellyfinURL, read_only: true });
      moduleSecrets.jellyfin = { api_key: jellyfinKey };
    }
    if (ccEnabled) {
      modules.push({ id: "cc-connect", instance_id: projectName, enabled: true, work_dir: workDir, read_only: false });
      moduleSecrets[projectName] = { bot_id: botID, bot_secret: botSecret, allow_from: allowFrom, admin_from: adminFrom };
    }
    try {
      const result = await api<{ join_info: string }>("/api/v1/admin/invites", {
        method: "POST",
        body: JSON.stringify({
          device_name: deviceName,
          agent_url: agentURL,
          modules,
          shared_directories: filebrowser ? [{ id: "default", name: "文件", permissions: ["read", "download"] }] : [],
          module_secrets: moduleSecrets,
        }),
      });
      setJoinInfo(result.join_info);
    } catch (reason) {
      setError(errorMessage(reason));
    } finally {
      setBusy(false);
    }
  }

  async function copyJoinInfo() {
    await navigator.clipboard.writeText(joinInfo);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1800);
  }

  return (
    <Page title="新设备">
      <form className="settings-form" onSubmit={createInvite}>
        <fieldset>
          <legend>设备</legend>
          <FormField label="名称"><input value={deviceName} onChange={(e) => setDeviceName(e.target.value)} required /></FormField>
          <FormField label="Agent HTTPS 地址"><input type="url" value={agentURL} onChange={(e) => setAgentURL(e.target.value)} placeholder="https://device.example.ts.net:9443" required /></FormField>
        </fieldset>
        <fieldset>
          <legend>模块</legend>
          <ModuleToggle label="文件" checked={filebrowser} onChange={setFilebrowser} />
          {filebrowser && <div className="module-fields"><FormField label="回环地址"><input value={filebrowserURL} onChange={(e) => setFilebrowserURL(e.target.value)} required /></FormField><FormField label="API Token"><input type="password" value={filebrowserToken} onChange={(e) => setFilebrowserToken(e.target.value)} required /></FormField></div>}
          <ModuleToggle label="影视" checked={jellyfin} onChange={setJellyfin} />
          {jellyfin && <div className="module-fields"><FormField label="回环地址"><input value={jellyfinURL} onChange={(e) => setJellyfinURL(e.target.value)} required /></FormField><FormField label="API Key"><input type="password" value={jellyfinKey} onChange={(e) => setJellyfinKey(e.target.value)} required /></FormField></div>}
          <ModuleToggle label="企业微信开发" checked={ccEnabled} onChange={setCCEnabled} />
          {ccEnabled && <div className="module-fields two-column">
            <FormField label="项目标识"><input value={projectName} onChange={(e) => setProjectName(e.target.value)} required /></FormField>
            <FormField label="工作目录"><input value={workDir} onChange={(e) => setWorkDir(e.target.value)} required /></FormField>
            <FormField label="Bot ID"><input value={botID} onChange={(e) => setBotID(e.target.value)} required /></FormField>
            <FormField label="Bot Secret"><input type="password" value={botSecret} onChange={(e) => setBotSecret(e.target.value)} required /></FormField>
            <FormField label="允许用户"><input value={allowFrom} onChange={(e) => setAllowFrom(e.target.value)} required /></FormField>
            <FormField label="管理员用户"><input value={adminFrom} onChange={(e) => setAdminFrom(e.target.value)} required /></FormField>
          </div>}
        </fieldset>
        <button className="primary-button" disabled={busy}>{busy ? <LoaderCircle className="spin" size={18} /> : <Plus size={18} />}{busy ? "生成中" : "生成连接信息"}</button>
      </form>
      {joinInfo && <div className="join-result"><code>{joinInfo}</code><button className="icon-button" onClick={() => void copyJoinInfo()} title="复制连接信息" aria-label="复制连接信息">{copied ? <Check size={18} /> : <Clipboard size={18} />}</button></div>}
      <Feedback error={error} />
    </Page>
  );
}

function AgentApp() {
  return (
    <AppFrame
      surface="设备"
      navigation={[
        { to: "/", label: "状态", icon: <MonitorCog size={18} /> },
        { to: "/files", label: "文件", icon: <FolderOpen size={18} /> },
        { to: "/media", label: "影视", icon: <Film size={18} /> },
      ]}
    >
      <Routes>
        <Route path="/" element={<AgentStatusPage />} />
        <Route path="/files" element={<FilesPage />} />
        <Route path="/media" element={<MediaPage />} />
        <Route path="*" element={<AgentStatusPage />} />
      </Routes>
    </AppFrame>
  );
}

function AgentStatusPage() {
  const [status, setStatus] = useState<DeviceStatus | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setError("");
    try { setStatus(await api<DeviceStatus>("/api/v1/status")); } catch (reason) { setError(errorMessage(reason)); }
  }, []);
  useEffect(() => void load(), [load]);
  return (
    <Page title={status?.name || "设备状态"} action={<button className="icon-button" onClick={() => void load()} title="刷新状态" aria-label="刷新状态"><RefreshCw size={18} /></button>}>
      {status && <>
        <div className="metrics-row">
          <StatusMetric icon={<Network size={18} />} label="连接" value={status.connection} tone={connectionTone(status.connection)} />
          <StatusMetric icon={<Server size={18} />} label="尾网地址" value={status.tailnet_ip || "-"} />
          <StatusMetric icon={<ShieldCheck size={18} />} label="配置" value={`revision ${status.config_revision}`} />
        </div>
        <div className="module-table">
          <div className="table-header"><span>模块</span><span>状态</span><span>版本</span></div>
          {status.modules.map((module) => <div className="table-row" key={module.id}><strong>{module.id}</strong><StatusPill label={module.state} tone={module.state === "ready" ? "good" : "warn"} /><span>{module.version || module.expected_version}</span></div>)}
        </div>
      </>}
      <Feedback error={error} />
    </Page>
  );
}

function FilesPage() {
  const navigate = useNavigate();
  const [currentPath, setCurrentPath] = useState("/");
  const [resource, setResource] = useState<FileResource | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async (target: string) => {
    setError("");
    try {
      const result = await api<FileResource>(`/api/v1/files/resources?path=${encodeURIComponent(target)}`);
      setResource(result);
      setCurrentPath(target);
    } catch (reason) { setError(errorMessage(reason)); }
  }, []);
  useEffect(() => void load("/"), [load]);
  const parent = useMemo(() => currentPath === "/" ? "/" : currentPath.split("/").slice(0, -1).join("/") || "/", [currentPath]);
  function child(name: string) { return `${currentPath === "/" ? "" : currentPath}/${name}`; }
  function download(item: FileItem) { location.assign(`/api/v1/files/raw?files=${encodeURIComponent(child(item.name))}`); }
  return (
    <Page title="文件" action={<button className="icon-button" onClick={() => void load(currentPath)} title="刷新文件" aria-label="刷新文件"><RefreshCw size={18} /></button>}>
      <div className="pathbar"><button className="icon-button" disabled={currentPath === "/"} onClick={() => void load(parent)} title="上一级" aria-label="上一级"><ChevronLeft size={18} /></button><code>{currentPath}</code></div>
      <div className="file-table">
        {[...(resource?.folders || []), ...(resource?.files || [])].map((item) => {
          const folder = item.type === "directory";
          return <button className="file-row" key={`${item.type}-${item.name}`} onClick={() => folder ? void load(child(item.name)) : download(item)}>
            <span className="file-name">{folder ? <Folder size={18} /> : <File size={18} />}<strong>{item.name}</strong></span>
            <span>{folder ? "文件夹" : formatBytes(item.size)}</span><span>{formatTime(item.modified)}</span>
          </button>;
        })}
      </div>
      {!resource && !error && <CenteredLoader label="读取文件" compact />}
      <Feedback error={error} />
      <button className="sr-only" onClick={() => navigate("/")}>返回状态</button>
    </Page>
  );
}

function MediaPage() {
  const [items, setItems] = useState<MediaItem[]>([]);
  const [selected, setSelected] = useState<MediaItem | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    try {
      const response = await api<{ Items: MediaItem[] }>("/api/v1/media/Items?Recursive=true&IncludeItemTypes=Movie,Episode&Fields=ProductionYear,RunTimeTicks,SeriesName&Limit=100");
      setItems(response.Items || []);
    } catch (reason) { setError(errorMessage(reason)); }
  }, []);
  useEffect(() => void load(), [load]);
  return (
    <Page title="影视" action={<button className="icon-button" onClick={() => void load()} title="刷新媒体" aria-label="刷新媒体"><RefreshCw size={18} /></button>}>
      {selected && <div className="player-band"><video controls autoPlay src={`/api/v1/media/Videos/${encodeURIComponent(selected.Id)}/stream.mp4?Static=true`} /><div><strong>{selected.Name}</strong><button className="text-button" onClick={() => setSelected(null)}>关闭</button></div></div>}
      <div className="media-grid">
        {items.map((item) => <button className="media-item" key={item.Id} onClick={() => setSelected(item)}>
          <img src={`/api/v1/media/Items/${encodeURIComponent(item.Id)}/Images/Primary?maxWidth=480&quality=85`} alt="" loading="lazy" />
          <span><strong>{item.Name}</strong><small>{item.SeriesName || item.ProductionYear || item.Type}</small></span>
        </button>)}
      </div>
      {!items.length && !error && <EmptyState icon={<Film size={24} />} label="媒体库为空" />}
      <Feedback error={error} />
    </Page>
  );
}

function AppFrame({ surface, navigation, footer, children }: { surface: string; navigation: Array<{ to: string; label: string; icon: React.ReactNode }>; footer?: React.ReactNode; children: React.ReactNode }) {
  return <div className="app-shell"><aside className="sidebar"><div className="brand"><span className="brand-mark">H</span><div><strong>HomeStack</strong><small>{surface}</small></div></div><nav>{navigation.map((item) => <NavLink key={item.to} to={item.to} end={item.to === "/"}>{item.icon}<span>{item.label}</span></NavLink>)}</nav><div className="sidebar-footer">{footer}</div></aside><main className="app-main">{children}</main></div>;
}

function Topbar({ surface }: { surface: string }) { return <header className="topbar"><div className="brand"><span className="brand-mark">H</span><strong>HomeStack</strong></div><span>{surface}</span></header>; }
function Page({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) { return <section className="page"><header className="page-header"><h1>{title}</h1>{action}</header>{children}</section>; }
function FormField({ label, children }: { label: string; children: React.ReactNode }) { return <label className="form-field"><span>{label}</span>{children}</label>; }
function ModuleToggle({ label, checked, onChange }: { label: string; checked: boolean; onChange: (value: boolean) => void }) { return <label className="module-toggle"><input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} /><span>{label}</span></label>; }
function StatusMetric({ icon, label, value, tone = "" }: { icon: React.ReactNode; label: string; value: string; tone?: string }) { return <div className={`status-metric ${tone}`}><span>{icon}</span><div><small>{label}</small><strong>{value}</strong></div></div>; }
function StatusPill({ label, tone }: { label: string; tone: string }) { return <span className={`status-pill ${tone}`}>{label}</span>; }
function Feedback({ message, error }: { message?: string; error?: string }) { return <>{message && <div className="feedback success"><Check size={17} />{message}</div>}{error && <InlineError message={error} />}</>; }
function InlineError({ message }: { message: string }) { return <div className="feedback error"><CircleAlert size={17} />{message}</div>; }
function EmptyState({ icon, label }: { icon: React.ReactNode; label: string }) { return <div className="empty-state">{icon}<span>{label}</span></div>; }
function CenteredLoader({ label, compact = false }: { label: string; compact?: boolean }) { return <div className={`centered-loader ${compact ? "compact" : ""}`}><LoaderCircle className="spin" size={22} /><span>{label}</span></div>; }
function connectionTone(connection?: string) { return connection === "直连" ? "good" : connection === "自有中继" ? "warn" : "muted"; }
function errorMessage(reason: unknown) { return reason instanceof Error ? reason.message : String(reason); }
