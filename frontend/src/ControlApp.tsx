import { Download, ExternalLink, Github, HardDrive, KeyRound, LogOut, Plus, RefreshCw, RotateCcw, Settings, Trash2, Unplug } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState, type ReactNode } from "react";
import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { api } from "./api";
import {
  AppShell, AuthLayout, Badge, Button, ConfirmDialog, EmptyState, IconButton, InlineNotice,
  Input, Loading, PageHeader, PasswordInput, Progress, ScrollArea, errorMessage,
} from "./components/ui";
import { normalizePublicAddress, type OAuthProviderID } from "./publicAddress";
import type { DeviceView } from "./types";

interface Me { subject: string; email: string; name: string; identities: { provider: OAuthProviderID; subject: string }[] }
interface Provider { id: OAuthProviderID; label: string }
interface ControlMetadata { providers: Provider[]; me: Me | null }
interface ProviderConfiguration extends Provider { configured: boolean; linked: boolean; client_id: string }
interface SystemConfiguration { public_host: string; providers: ProviderConfiguration[] }
interface ControlUpdateStatus {
  state: "idle" | "checking" | "up-to-date" | "available" | "downloading" | "verifying" | "ready" | "installing" | "error";
  current_version: string; latest_version?: string; published_at?: string; notes?: string;
  downloaded?: number; total?: number; signature: string; error?: string;
}

const controlNav = [
  { to: "/", label: "设备", icon: HardDrive, end: true },
  { to: "/activate", label: "激活", icon: Plus },
  { to: "/identity", label: "身份", icon: KeyRound },
  { to: "/settings/domains", label: "域名", icon: Settings },
  { to: "/settings/updates", label: "更新", icon: Download },
];

export function ControlApp() {
  const routeLocation = useLocation();
  const [me, setMe] = useState<Me | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [checked, setChecked] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    api<ControlMetadata>("/api/meta")
      .then((meta) => { setProviders(meta.providers); setMe(meta.me); })
      .catch((reason) => setError(errorMessage(reason)))
      .finally(() => setChecked(true));
  }, []);
  if (!checked) return <Loading label="正在验证身份" />;
  if (!me) {
    if (routeLocation.pathname !== "/login") return <Navigate to="/login" replace />;
    return <AuthLayout title="登录 HomeStack" description="使用此服务器已配置的身份继续"><div className="control-provider-list">{providers.map((provider) => <Button asChild fullWidth key={provider.id} size="lg" tone="default"><a href={`/auth/login/${encodeURIComponent(provider.id)}?return=/`}><ProviderIcon provider={provider.id} />使用 {provider.label} 登录</a></Button>)}</div>{error && <InlineNotice tone="danger">{error}</InlineNotice>}</AuthLayout>;
  }
  if (routeLocation.pathname === "/login") return <Navigate to="/" replace />;
  async function logout() {
    try { await api<void>("/auth/logout", { method: "POST" }); location.assign("/"); }
    catch (reason) { setError(errorMessage(reason)); }
  }
  const currentProvider = me.identities[0]?.provider;
  return <AppShell actions={<Button onClick={() => void logout()} tone="default" variant="ghost"><LogOut size={16} />退出</Button>} nav={controlNav} product="Control"><Routes><Route path="/" element={<ControlDevices me={me} />} /><Route path="/activate" element={<ActivationPage />} /><Route path="/identity" element={<IdentityPage me={me} />} /><Route path="/settings/domains" element={<DomainSettings provider={currentProvider ? { id: currentProvider, label: currentProvider === "google" ? "Google" : "GitHub" } : undefined} />} /><Route path="/settings/updates" element={<ControlUpdates />} /><Route path="*" element={<Navigate to="/" replace />} /></Routes>{error && <div className="page"><InlineNotice tone="danger">{error}</InlineNotice></div>}</AppShell>;
}

export function ControlUpdates() {
  const [status, setStatus] = useState<ControlUpdateStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(async () => setStatus(await api<ControlUpdateStatus>("/api/system/updates/status")), []);
  useEffect(() => { void load().catch((reason) => setError(errorMessage(reason))); }, [load]);
  async function operation(path: "check" | "download" | "install") {
    setBusy(true); setError("");
    try {
      const next = await api<ControlUpdateStatus>(`/api/system/updates/${path}`, { method: "POST", body: "{}" });
      setStatus(next);
      if (path === "install") await waitForControlHealth();
    } catch (reason) { setError(errorMessage(reason)); }
    finally { setBusy(false); }
  }
  if (!status) return <Page title="VPS 更新"><Loading label="读取更新状态" compact />{error && <InlineNotice tone="danger">{error}</InlineNotice>}</Page>;
  const percent = status.total ? Math.min(100, Math.round(((status.downloaded || 0) / status.total) * 100)) : 0;
  return <Page title="VPS 更新"><div className="update-grid"><span>当前版本</span><strong>{status.current_version}</strong><span>最新版本</span><strong>{status.latest_version || "-"}</strong><span>状态</span><strong>{controlUpdateLabel(status.state)}</strong><span>签名</span><strong>{status.signature}</strong></div>{status.published_at && <p className="muted">发布于 {new Date(status.published_at).toLocaleString("zh-CN")}</p>}{status.notes && <ScrollArea className="release-notes">{status.notes}</ScrollArea>}{status.state === "downloading" && <Progress label="更新下载进度" value={percent} />}{status.state === "installing" && <InlineNotice>Control 正在重启，服务恢复后页面会自动刷新。</InlineNotice>}<div className="button-row"><Button disabled={busy || status.state === "installing"} loading={busy && status.state !== "available" && status.state !== "ready"} onClick={() => void operation("check")} tone="default" variant="outline"><RefreshCw size={16} />检查更新</Button>{status.state === "available" && <Button disabled={busy} loading={busy} onClick={() => void operation("download")}><Download size={16} />下载并校验</Button>}{status.state === "ready" && <Button disabled={busy} loading={busy} onClick={() => void operation("install")}><RotateCcw size={16} />安装并重启</Button>}</div>{(error || status.error) && <InlineNotice tone="danger">{error || status.error}</InlineNotice>}</Page>;
}

async function waitForControlHealth() {
  const deadline = Date.now() + 90_000;
  let lastError = "Control 尚未恢复";
  while (Date.now() < deadline) {
    try {
      const response = await fetch("/api/health", { cache: "no-store", credentials: "same-origin" });
      if (response.ok && response.headers.get("content-type")?.includes("application/json")) { location.reload(); return; }
      lastError = `健康接口返回 HTTP ${response.status}`;
    } catch (reason) { lastError = errorMessage(reason); }
    await new Promise((resolve) => window.setTimeout(resolve, 1000));
  }
  throw new Error(`Control 重启后健康检查超时：${lastError}`);
}

function controlUpdateLabel(state: ControlUpdateStatus["state"]) { return ({ idle: "空闲", checking: "检查中", "up-to-date": "已是最新", available: "有更新", downloading: "下载中", verifying: "签名校验中", ready: "可以安装", installing: "正在重启", error: "错误" })[state]; }

function ProviderIcon({ provider }: { provider: OAuthProviderID }) {
  if (provider === "github") return <Github size={21} strokeWidth={2.2} aria-hidden="true" />;
  return <svg className="google-icon" viewBox="0 0 24 24" aria-hidden="true"><path fill="#4285F4" d="M21.6 12.23c0-.71-.06-1.4-.18-2.07H12v3.92h5.38a4.6 4.6 0 0 1-2 3.02v2.54h3.24c1.9-1.75 2.98-4.33 2.98-7.41Z"/><path fill="#34A853" d="M12 22c2.7 0 4.97-.9 6.62-2.43l-3.24-2.54c-.9.6-2.05.96-3.38.96-2.61 0-4.82-1.76-5.61-4.13H3.04v2.62A10 10 0 0 0 12 22Z"/><path fill="#FBBC05" d="M6.39 13.86A6 6 0 0 1 6.08 12c0-.65.11-1.28.31-1.86V7.52H3.04A10 10 0 0 0 2 12c0 1.61.39 3.14 1.04 4.48l3.35-2.62Z"/><path fill="#EA4335" d="M12 6.01c1.47 0 2.79.51 3.82 1.5l2.87-2.87A9.64 9.64 0 0 0 12 2a10 10 0 0 0-8.96 5.52l3.35 2.62C7.18 7.77 9.39 6.01 12 6.01Z"/></svg>;
}

function ControlDevices({ me }: { me: Me }) {
  const [devices, setDevices] = useState<DeviceView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [removing, setRemoving] = useState<DeviceView | null>(null);
  const load = useCallback(async () => { setLoading(true); setError(""); try { setDevices((await api<{ devices: DeviceView[] }>("/api/devices")).devices); } catch (reason) { setError(errorMessage(reason)); } finally { setLoading(false); } }, []);
  useEffect(() => { void load(); }, [load]);
  async function remove() { if (!removing) return; setError(""); try { await api<void>(`/api/devices/${encodeURIComponent(removing.id)}`, { method: "DELETE" }); setRemoving(null); await load(); } catch (reason) { setError(errorMessage(reason)); } }
  return <Page title="设备" description={me.name || me.email} action={<IconButton label="刷新设备" onClick={() => void load()}><RefreshCw size={16} /></IconButton>}>{loading ? <Loading label="读取设备" compact /> : devices.length === 0 ? <EmptyState icon={<Unplug size={22} />} title="暂无设备" description="激活 HomeStack App 或 Node 后会显示在这里。" /> : <div className="device-list">{devices.map((device) => <article className="device-row" key={device.id}><span className="device-icon"><HardDrive size={19} /></span><div className="device-copy"><strong>{device.name}</strong><small>{device.status?.tailscale_ip || device.magic_dns || device.agent_url}</small></div><Badge tone={device.status?.online ? "success" : "neutral"}>{device.status?.online ? "在线" : "离线"}</Badge><Badge tone={connectionTone(device.status?.connection)}>{device.status?.connection || "未连接"}</Badge><IconButton asChild label={`打开 ${device.name}`}><a href={`/devices/${encodeURIComponent(device.id)}/open`}><ExternalLink size={17} /></a></IconButton><IconButton label={`移除 ${device.name}`} onClick={() => setRemoving(device)} tone="danger"><Trash2 size={16} /></IconButton></article>)}</div>}{error && <InlineNotice tone="danger">{error}</InlineNotice>}<ConfirmDialog description={`设备“${removing?.name || ""}”将从 Control 中移除。`} onConfirm={remove} onOpenChange={(open) => !open && setRemoving(null)} open={Boolean(removing)} title="移除设备" /></Page>;
}

function ActivationPage() {
  const [result, setResult] = useState<{ code: string; expires_at: string } | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function create() { setBusy(true); setError(""); try { setResult(await api<{ code: string; expires_at: string }>("/api/device-activations", { method: "POST", body: "{}" })); } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); } }
  return <Page title="激活 App 与 Node" description="在已安装 HomeStack 的设备中填写此单次激活码。">{result ? <div className="settings-list"><div><span>激活码</span><strong>{result.code}</strong></div><div><span>有效期</span><strong>{new Date(result.expires_at).toLocaleString("zh-CN")}</strong></div></div> : <Button loading={busy} onClick={() => void create()}><Plus size={16} />生成激活码</Button>}{error && <InlineNotice tone="danger">{error}</InlineNotice>}</Page>;
}

function IdentityPage({ me }: { me: Me }) {
  const routeLocation = useLocation();
  const [configuration, setConfiguration] = useState<SystemConfiguration | null>(null);
  const [clientID, setClientID] = useState(""); const [clientSecret, setClientSecret] = useState(""); const [confirmation, setConfirmation] = useState(""); const [error, setError] = useState("");
  const reauthenticated = new URLSearchParams(routeLocation.search).get("reauthenticated") === "1";
  useEffect(() => { api<SystemConfiguration>("/api/system/config").then(setConfiguration).catch((reason) => setError(errorMessage(reason))); }, []);
  const target = configuration?.providers.find((provider) => !provider.configured);
  const currentID = me.identities[0]?.provider || "";
  const currentLabel = currentID === "google" ? "Google" : "GitHub";
  async function submit(event: FormEvent) { event.preventDefault(); if (!target) return; setError(""); try { const result = await api<{ authorization_url: string }>(`/api/system/providers/${encodeURIComponent(target.id)}/link`, { method: "POST", body: JSON.stringify({ client_id: clientID, client_secret: clientSecret, confirmation }) }); location.assign(result.authorization_url); } catch (reason) { setError(errorMessage(reason)); } }
  return <Page title="登录身份"><div className="settings-list"><div><span>Owner</span><strong>{me.name || me.email}</strong></div>{configuration?.providers.map((provider) => <div key={provider.id}><span>{provider.label}</span><Badge tone={provider.linked ? "success" : provider.configured ? "info" : "neutral"}>{provider.linked ? "已绑定" : provider.configured ? "已配置" : "未配置"}</Badge></div>)}</div>{target && <form className="form-grid provider-link-form" onSubmit={(event) => void submit(event)}><h2>绑定 {target.label}</h2><label>OAuth 回调地址<output>{`${location.origin}/auth/callback/${target.id}`}</output></label><label>OAuth Client ID<Input value={clientID} onChange={(event) => setClientID(event.target.value)} required /></label><label>OAuth Client Secret<PasswordInput autoComplete="new-password" value={clientSecret} onChange={(event) => setClientSecret(event.target.value)} required /></label><Button asChild tone="default" variant="outline"><a href={`/auth/reauth/${encodeURIComponent(currentID)}?return=/identity`}>{reauthenticated ? "已重新认证" : `使用 ${currentLabel} 重新认证`}</a></Button><label>输入 {target.id} 确认<Input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label><Button disabled={!reauthenticated || confirmation !== target.id} type="submit"><KeyRound size={16} />验证并绑定 {target.label}</Button></form>}{error && <InlineNotice tone="danger">{error}</InlineNotice>}</Page>;
}

function DomainSettings({ provider }: { provider?: Provider }) {
  const routeLocation = useLocation();
  const [host, setHost] = useState(""); const [confirmation, setConfirmation] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const reauthenticated = new URLSearchParams(routeLocation.search).get("reauthenticated") === "1";
  useEffect(() => { api<{ public_host: string }>("/api/system/config").then((config) => setHost(config.public_host)).catch((reason) => setError(errorMessage(reason))); }, []);
  async function submit(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); try { const result = await api<{ target_url: string }>("/api/system/reconfigure", { method: "POST", body: JSON.stringify({ public_host: host, confirmation }) }); location.assign(result.target_url); } catch (reason) { setError(errorMessage(reason)); setBusy(false); } }
  const target = normalizePublicAddress(host); const confirmed = normalizePublicAddress(confirmation); const matches = Boolean(target && confirmed && target.host === confirmed.host);
  return <Page title="VPS 域名"><form className="form-grid" onSubmit={(event) => void submit(event)}><label>新域名<Input value={host} onChange={(event) => setHost(event.target.value)} required /></label><Button asChild tone="default" variant="outline"><a href={`/auth/reauth/${encodeURIComponent(provider?.id || "")}`}>{reauthenticated ? "已重新认证" : `使用 ${provider?.label || "当前登录方式"} 重新认证`}</a></Button><label>输入新域名确认<Input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label><Button disabled={busy || !reauthenticated || !matches} loading={busy} type="submit"><Settings size={16} />应用域名</Button></form>{error && <InlineNotice tone="danger">{error}</InlineNotice>}</Page>;
}

function Page({ action, children, description, title }: { action?: ReactNode; children: ReactNode; description?: ReactNode; title: ReactNode }) {
  return <section className="page"><PageHeader action={action} description={description} title={title} />{children}</section>;
}

function connectionTone(connection?: string): "success" | "warning" | "neutral" { return connection === "直连" ? "success" : connection === "自有中继" ? "warning" : "neutral"; }
