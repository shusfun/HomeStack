import { Download, ExternalLink, Github, HardDrive, KeyRound, LogOut, Plus, RefreshCw, RotateCcw, Settings, Trash2, Unplug } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState } from "react";
import { Navigate, NavLink, Route, Routes } from "react-router-dom";
import { api } from "./api";
import { BrandMark } from "./BrandMark";
import { normalizePublicAddress, type OAuthProviderID } from "./publicAddress";
import type { DeviceView } from "./types";
import { CenteredLoader, EmptyState, Feedback, StatusPill, connectionTone, errorMessage } from "./ui";

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

export function ControlApp() {
  const [me, setMe] = useState<Me | null>(null); const [providers, setProviders] = useState<Provider[]>([]);
  const [checked, setChecked] = useState(false); const [error, setError] = useState("");
  useEffect(() => {
    api<ControlMetadata>("/api/meta")
      .then((meta) => { setProviders(meta.providers); setMe(meta.me); }).catch((reason) => setError(errorMessage(reason))).finally(() => setChecked(true));
  }, []);
  if (!checked) return <CenteredLoader label="验证身份" />;
  if (!me) {
    if (location.pathname !== "/login") return <Navigate to="/login" replace />;
    return <main className="control-login"><div className="control-login-content"><div className="control-login-brand"><BrandMark className="login-mark" /></div><h1>登录 HomeStack</h1><p>使用此服务器已配置的身份继续</p><div className={`control-provider-list${providers.length === 1 ? " single" : ""}`}>{providers.map((provider) => <a key={provider.id} href={`/auth/login/${encodeURIComponent(provider.id)}?return=/`}><ProviderIcon provider={provider.id} />使用 {provider.label} 登录</a>)}</div><Feedback error={error} /></div></main>;
  }
  if (location.pathname === "/login") return <Navigate to="/" replace />;
  async function logout() { try { await api<void>("/auth/logout", { method: "POST" }); location.assign("/"); } catch (reason) { setError(errorMessage(reason)); } }
  const currentProvider = me.identities[0]?.provider;
  return <div className="control-shell"><header><div className="chrome-brand"><BrandMark className="brand-mark" /><strong>HomeStack</strong><small>Control</small></div><nav><NavLink to="/"><HardDrive size={16} />设备</NavLink><NavLink to="/activate"><Plus size={16} />激活</NavLink><NavLink to="/identity"><KeyRound size={16} />身份</NavLink><NavLink to="/settings/domains"><Settings size={16} />域名</NavLink><NavLink to="/settings/updates"><Download size={16} />更新</NavLink><button onClick={() => void logout()}><LogOut size={16} />退出</button></nav></header><main><Routes><Route path="/" element={<ControlDevices me={me} />} /><Route path="/activate" element={<ActivationPage />} /><Route path="/identity" element={<IdentityPage me={me} />} /><Route path="/settings/domains" element={<DomainSettings provider={currentProvider ? { id: currentProvider, label: currentProvider === "google" ? "Google" : "GitHub" } : undefined} />} /><Route path="/settings/updates" element={<ControlUpdates />} /><Route path="*" element={<ControlDevices me={me} />} /></Routes><Feedback error={error} /></main></div>;
}

export function ControlUpdates() {
  const [status, setStatus] = useState<ControlUpdateStatus | null>(null); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const load = useCallback(async () => { setStatus(await api<ControlUpdateStatus>("/api/system/updates/status")); }, []);
  useEffect(() => { void load().catch((reason) => setError(errorMessage(reason))); }, [load]);
  async function operation(path: "check" | "download" | "install") {
    setBusy(true); setError("");
    try {
      const next = await api<ControlUpdateStatus>(`/api/system/updates/${path}`, { method: "POST", body: "{}" });
      setStatus(next);
      if (path === "install") window.setTimeout(() => location.reload(), 5000);
    } catch (reason) { setError(errorMessage(reason)); }
    finally { setBusy(false); }
  }
  if (!status) return <section className="page"><h1>VPS 更新</h1><CenteredLoader label="读取更新状态" compact /><Feedback error={error} /></section>;
  const percent = status.total ? Math.min(100, Math.round(((status.downloaded || 0) / status.total) * 100)) : 0;
  return <section className="page"><h1>VPS 更新</h1><div className="update-grid"><span>当前版本</span><strong>{status.current_version}</strong><span>最新版本</span><strong>{status.latest_version || "-"}</strong><span>状态</span><strong>{controlUpdateLabel(status.state)}</strong><span>签名</span><strong>{status.signature}</strong></div>{status.published_at && <p className="muted">发布于 {new Date(status.published_at).toLocaleString("zh-CN")}</p>}{status.notes && <pre className="release-notes">{status.notes}</pre>}{status.state === "downloading" && <div className="progress"><span style={{ width: `${percent}%` }} /></div>}<div className="button-row"><button className="secondary-button" disabled={busy || status.state === "installing"} onClick={() => void operation("check")}><RefreshCw size={16} />检查更新</button>{status.state === "available" && <button className="primary-button" disabled={busy} onClick={() => void operation("download")}><Download size={16} />下载并校验</button>}{status.state === "ready" && <button className="primary-button" disabled={busy} onClick={() => void operation("install")}><RotateCcw size={16} />安装并重启</button>}</div><Feedback error={error || status.error} /></section>;
}

function controlUpdateLabel(state: ControlUpdateStatus["state"]) { return ({ idle: "空闲", checking: "检查中", "up-to-date": "已是最新", available: "有更新", downloading: "下载中", verifying: "签名校验中", ready: "可以安装", installing: "正在重启", error: "错误" })[state]; }

function ProviderIcon({ provider }: { provider: OAuthProviderID }) {
  if (provider === "github") return <Github size={21} strokeWidth={2.2} aria-hidden="true" />;
  return <svg className="google-icon" viewBox="0 0 24 24" aria-hidden="true"><path fill="#4285F4" d="M21.6 12.23c0-.71-.06-1.4-.18-2.07H12v3.92h5.38a4.6 4.6 0 0 1-2 3.02v2.54h3.24c1.9-1.75 2.98-4.33 2.98-7.41Z"/><path fill="#34A853" d="M12 22c2.7 0 4.97-.9 6.62-2.43l-3.24-2.54c-.9.6-2.05.96-3.38.96-2.61 0-4.82-1.76-5.61-4.13H3.04v2.62A10 10 0 0 0 12 22Z"/><path fill="#FBBC05" d="M6.39 13.86A6 6 0 0 1 6.08 12c0-.65.11-1.28.31-1.86V7.52H3.04A10 10 0 0 0 2 12c0 1.61.39 3.14 1.04 4.48l3.35-2.62Z"/><path fill="#EA4335" d="M12 6.01c1.47 0 2.79.51 3.82 1.5l2.87-2.87A9.64 9.64 0 0 0 12 2a10 10 0 0 0-8.96 5.52l3.35 2.62C7.18 7.77 9.39 6.01 12 6.01Z"/></svg>;
}

function ControlDevices({ me }: { me: Me }) {
  const [devices, setDevices] = useState<DeviceView[]>([]); const [loading, setLoading] = useState(true); const [error, setError] = useState("");
  const load = useCallback(async () => { setLoading(true); setError(""); try { setDevices((await api<{ devices: DeviceView[] }>("/api/devices")).devices); } catch (reason) { setError(errorMessage(reason)); } finally { setLoading(false); } }, []);
  useEffect(() => { void load(); }, [load]);
  async function remove(device: DeviceView) { if (!window.confirm(`移除 ${device.name}？`)) return; setError(""); try { await api<void>(`/api/devices/${encodeURIComponent(device.id)}`, { method: "DELETE" }); await load(); } catch (reason) { setError(errorMessage(reason)); } }
  return <section className="page"><div className="summary-line"><div><h1>设备</h1><p>{me.name || me.email}</p></div><button className="icon-button" onClick={() => void load()} title="刷新" aria-label="刷新"><RefreshCw size={16} /></button></div>{loading ? <CenteredLoader label="读取设备" compact /> : devices.length === 0 ? <EmptyState icon={<Unplug size={24} />} label="暂无设备" /> : <div className="device-list">{devices.map((device) => <article className="device-row" key={device.id}><span className="device-icon"><HardDrive size={19} /></span><div className="device-copy"><strong>{device.name}</strong><small>{device.status?.tailscale_ip || device.magic_dns || device.agent_url}</small></div><StatusPill label={device.status?.online ? "在线" : "离线"} tone={device.status?.online ? "good" : "muted"} /><StatusPill label={device.status?.connection || "未连接"} tone={connectionTone(device.status?.connection)} /><a className="icon-button" href={`/devices/${encodeURIComponent(device.id)}/open`} title="打开设备" aria-label={`打开 ${device.name}`}><ExternalLink size={17} /></a><button className="icon-button" onClick={() => void remove(device)} title="移除设备" aria-label={`移除 ${device.name}`}><Trash2 size={16} /></button></article>)}</div>}<Feedback error={error} /></section>;
}

function ActivationPage() {
  const [result, setResult] = useState<{ code: string; expires_at: string } | null>(null); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  async function create() { setBusy(true); setError(""); try { setResult(await api<{ code: string; expires_at: string }>("/api/device-activations", { method: "POST", body: "{}" })); } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); } }
  return <section className="page"><h1>激活 App 与 Node</h1><p className="muted">在已安装 HomeStack 的设备中填写此单次激活码。</p>{result ? <div className="settings-list"><div><span>激活码</span><strong>{result.code}</strong></div><div><span>有效期</span><strong>{new Date(result.expires_at).toLocaleString("zh-CN")}</strong></div></div> : <button className="primary-button" disabled={busy} onClick={() => void create()}><Plus size={16} />生成激活码</button>}<Feedback error={error} /></section>;
}

function IdentityPage({ me }: { me: Me }) {
  const [configuration, setConfiguration] = useState<SystemConfiguration | null>(null);
  const [clientID, setClientID] = useState(""); const [clientSecret, setClientSecret] = useState(""); const [confirmation, setConfirmation] = useState(""); const [error, setError] = useState("");
  const reauthenticated = new URLSearchParams(location.search).get("reauthenticated") === "1";
  useEffect(() => { api<SystemConfiguration>("/api/system/config").then(setConfiguration).catch((reason) => setError(errorMessage(reason))); }, []);
  const target = configuration?.providers.find((provider) => !provider.configured);
  const currentID = me.identities[0]?.provider || "";
  const currentLabel = currentID === "google" ? "Google" : "GitHub";
  async function submit(event: FormEvent) { event.preventDefault(); if (!target) return; setError(""); try { const result = await api<{ authorization_url: string }>(`/api/system/providers/${encodeURIComponent(target.id)}/link`, { method: "POST", body: JSON.stringify({ client_id: clientID, client_secret: clientSecret, confirmation }) }); location.assign(result.authorization_url); } catch (reason) { setError(errorMessage(reason)); } }
  return <section className="page"><h1>登录身份</h1><div className="settings-list"><div><span>Owner</span><strong>{me.name || me.email}</strong></div>{configuration?.providers.map((provider) => <div key={provider.id}><span>{provider.label}</span><strong>{provider.linked ? "已绑定" : provider.configured ? "已配置" : "未配置"}</strong></div>)}</div>{target && <form className="form-grid provider-link-form" onSubmit={(event) => void submit(event)}><h2>绑定 {target.label}</h2><label>OAuth 回调地址<output>{`${location.origin}/auth/callback/${target.id}`}</output></label><label>OAuth Client ID<input value={clientID} onChange={(event) => setClientID(event.target.value)} required /></label><label>OAuth Client Secret<input type="password" autoComplete="new-password" value={clientSecret} onChange={(event) => setClientSecret(event.target.value)} required /></label><a className="secondary-button" href={`/auth/reauth/${encodeURIComponent(currentID)}?return=/identity`}>{reauthenticated ? "已重新认证" : `使用 ${currentLabel} 重新认证`}</a><label>输入 {target.id} 确认<input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label><button className="primary-button" disabled={!reauthenticated || confirmation !== target.id}><KeyRound size={16} />验证并绑定 {target.label}</button></form>}<Feedback error={error} /></section>;
}

function DomainSettings({ provider }: { provider?: Provider }) {
  const [host, setHost] = useState(""); const [confirmation, setConfirmation] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const reauthenticated = new URLSearchParams(location.search).get("reauthenticated") === "1";
  useEffect(() => { api<{ public_host: string }>("/api/system/config").then((config) => setHost(config.public_host)).catch((reason) => setError(errorMessage(reason))); }, []);
  async function submit(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); try { const result = await api<{ target_url: string }>("/api/system/reconfigure", { method: "POST", body: JSON.stringify({ public_host: host, confirmation }) }); location.assign(result.target_url); } catch (reason) { setError(errorMessage(reason)); setBusy(false); } }
  const target = normalizePublicAddress(host); const confirmed = normalizePublicAddress(confirmation); const matches = Boolean(target && confirmed && target.host === confirmed.host);
  return <section className="page"><h1>VPS 域名</h1><form className="form-grid" onSubmit={(event) => void submit(event)}><label>新域名<input value={host} onChange={(event) => setHost(event.target.value)} required /></label><a className="secondary-button" href={`/auth/reauth/${encodeURIComponent(provider?.id || "")}`}>{reauthenticated ? "已重新认证" : `使用 ${provider?.label || "当前登录方式"} 重新认证`}</a><label>输入新域名确认<input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label><button className="primary-button" disabled={busy || !reauthenticated || !matches}><Settings size={16} />应用域名</button></form><Feedback error={error} /></section>;
}
