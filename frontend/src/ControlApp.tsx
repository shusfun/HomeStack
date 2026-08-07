import { ExternalLink, HardDrive, KeyRound, LogIn, LogOut, Plus, RefreshCw, Settings, Trash2, Unplug } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState } from "react";
import { NavLink, Route, Routes } from "react-router-dom";
import { api } from "./api";
import { BrandMark } from "./BrandMark";
import type { DeviceView } from "./types";
import { CenteredLoader, EmptyState, Feedback, StatusPill, connectionTone, errorMessage } from "./ui";

interface Me { subject: string; email: string; name: string; identities: { provider: string; subject: string }[] }
interface Provider { id: string; label: string }

export function ControlApp() {
  const [me, setMe] = useState<Me | null>(null); const [providers, setProviders] = useState<Provider[]>([]);
  const [checked, setChecked] = useState(false); const [error, setError] = useState("");
  const switchingProvider = new URLSearchParams(location.search).has("provider_switch");
  useEffect(() => { if (!switchingProvider) return; const timer = window.setTimeout(() => location.replace("/"), 3500); return () => window.clearTimeout(timer); }, [switchingProvider]);
  useEffect(() => {
    Promise.all([api<{ providers: Provider[] }>("/api/meta"), api<Me>("/api/me").catch(() => null)])
      .then(([meta, identity]) => { setProviders(meta.providers); setMe(identity); }).catch((reason) => setError(errorMessage(reason))).finally(() => setChecked(true));
  }, []);
  if (switchingProvider) return <CenteredLoader label="切换登录方式" />;
  if (!checked) return <CenteredLoader label="验证身份" />;
  if (!me) return <main className="control-login"><BrandMark className="login-mark" /><h1>HomeStack Control</h1><div className="provider-list">{providers.map((provider) => <a key={provider.id} href={`/auth/login/${encodeURIComponent(provider.id)}?return=/`}><LogIn size={16} />{provider.label}</a>)}</div><Feedback error={error} /></main>;
  async function logout() { try { await api<void>("/auth/logout", { method: "POST" }); location.assign("/"); } catch (reason) { setError(errorMessage(reason)); } }
  return <div className="control-shell"><header><div className="chrome-brand"><BrandMark className="brand-mark" /><strong>HomeStack</strong><small>Control</small></div><nav><NavLink to="/"><HardDrive size={16} />设备</NavLink><NavLink to="/activate"><Plus size={16} />激活</NavLink><NavLink to="/identity"><KeyRound size={16} />身份</NavLink><NavLink to="/settings/domains"><Settings size={16} />域名</NavLink><button onClick={() => void logout()}><LogOut size={16} />退出</button></nav></header><main><Routes><Route path="/" element={<ControlDevices me={me} />} /><Route path="/activate" element={<ActivationPage />} /><Route path="/identity" element={<IdentityPage me={me} providers={providers} />} /><Route path="/settings/domains" element={<DomainSettings provider={providers[0]} />} /><Route path="*" element={<ControlDevices me={me} />} /></Routes><Feedback error={error} /></main></div>;
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

function IdentityPage({ me, providers }: { me: Me; providers: Provider[] }) {
  const current = providers[0]; const target = current?.id === "google" ? "github" : "google";
  const [clientID, setClientID] = useState(""); const [clientSecret, setClientSecret] = useState(""); const [confirmation, setConfirmation] = useState(""); const [error, setError] = useState("");
  const reauthenticated = new URLSearchParams(location.search).get("reauthenticated") === "1";
  async function submit(event: FormEvent) { event.preventDefault(); setError(""); try { const result = await api<{ authorization_url: string }>("/api/system/provider-switch", { method: "POST", body: JSON.stringify({ provider: target, client_id: clientID, client_secret: clientSecret, confirmation }) }); location.assign(result.authorization_url); } catch (reason) { setError(errorMessage(reason)); } }
  return <section className="page"><h1>登录身份</h1><div className="settings-list"><div><span>登录方式</span><strong>{current?.label || "-"}</strong></div><div><span>Owner</span><strong>{me.name || me.email}</strong></div></div><form className="form-grid" onSubmit={(event) => void submit(event)}><h2>切换到 {target === "google" ? "Google" : "GitHub"}</h2><label>新 OAuth 回调地址<output>{`${location.origin}/auth/provider-switch/callback/${target}`}</output></label><label>OAuth Client ID<input value={clientID} onChange={(event) => setClientID(event.target.value)} required /></label><label>OAuth Client Secret<input type="password" value={clientSecret} onChange={(event) => setClientSecret(event.target.value)} required /></label><a className="secondary-button" href={`/auth/reauth/${encodeURIComponent(current?.id || "")}?return=/identity`}>{reauthenticated ? "已重新认证" : `使用 ${current?.label || "当前登录方式"} 重新认证`}</a><label>输入 {target} 确认<input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label><button className="primary-button" disabled={!reauthenticated || confirmation !== target}><KeyRound size={16} />验证新登录源</button></form><Feedback error={error} /></section>;
}

function DomainSettings({ provider }: { provider?: Provider }) {
  const [host, setHost] = useState(""); const [confirmation, setConfirmation] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const reauthenticated = new URLSearchParams(location.search).get("reauthenticated") === "1";
  useEffect(() => { api<{ public_host: string }>("/api/system/config").then((config) => setHost(config.public_host)).catch((reason) => setError(errorMessage(reason))); }, []);
  async function submit(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); try { const result = await api<{ target_url: string }>("/api/system/reconfigure", { method: "POST", body: JSON.stringify({ public_host: host, confirmation }) }); location.assign(result.target_url); } catch (reason) { setError(errorMessage(reason)); setBusy(false); } }
  return <section className="page"><h1>VPS 域名</h1><form className="form-grid" onSubmit={(event) => void submit(event)}><label>新域名<input value={host} onChange={(event) => setHost(event.target.value)} required /></label><a className="secondary-button" href={`/auth/reauth/${encodeURIComponent(provider?.id || "")}`}>{reauthenticated ? "已重新认证" : `使用 ${provider?.label || "当前登录方式"} 重新认证`}</a><label>输入新域名确认<input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label><button className="primary-button" disabled={busy || !reauthenticated || confirmation !== host}><Settings size={16} />应用域名</button></form><Feedback error={error} /></section>;
}
