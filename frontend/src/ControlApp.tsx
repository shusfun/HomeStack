import { Check, ExternalLink, HardDrive, KeyRound, LoaderCircle, LogIn, LogOut, Plus, RefreshCw, Settings, ShieldCheck, Unplug } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState } from "react";
import { NavLink, Route, Routes } from "react-router-dom";
import { api } from "./api";
import { BrandMark } from "./BrandMark";
import { EnrollmentForm, type EnrollmentPolicy } from "./EnrollmentForm";
import type { DeviceView } from "./types";
import { CenteredLoader, EmptyState, Feedback, StatusPill, connectionTone, errorMessage } from "./ui";

interface Me { subject: string; email: string; name: string; identities: { provider: string; subject: string }[] }
interface Provider { id: string; label: string }

export function ControlApp() {
  const [me, setMe] = useState<Me | null>(null); const [providers, setProviders] = useState<Provider[]>([]);
  const [checked, setChecked] = useState(false); const [error, setError] = useState("");
  useEffect(() => {
    Promise.all([api<{ providers: Provider[] }>("/api/v1/meta"), api<Me>("/api/v1/me").catch(() => null)])
      .then(([meta, identity]) => { setProviders(meta.providers); setMe(identity); }).catch((reason) => setError(errorMessage(reason))).finally(() => setChecked(true));
  }, []);
  if (!checked) return <CenteredLoader label="验证身份" />;
  if (!me) return <main className="control-login"><BrandMark className="login-mark" /><h1>HomeStack Control</h1><div className="provider-list">{providers.map((provider) => <a key={provider.id} href={`/auth/login/${encodeURIComponent(provider.id)}?return=/`}><LogIn size={16} />{provider.label}</a>)}</div><Feedback error={error} /></main>;
  async function logout() { try { await api<void>("/auth/logout", { method: "POST" }); location.assign("/"); } catch (reason) { setError(errorMessage(reason)); } }
  return <div className="control-shell"><header><div className="chrome-brand"><BrandMark className="brand-mark" /><strong>HomeStack</strong><small>Control</small></div><nav><NavLink to="/"><HardDrive size={16} />设备</NavLink><NavLink to="/enroll"><Plus size={16} />配对</NavLink><NavLink to="/identities"><KeyRound size={16} />身份</NavLink><NavLink to="/settings/domains"><Settings size={16} />设置</NavLink><button onClick={() => void logout()}><LogOut size={16} />退出</button></nav></header><main><Routes><Route path="/" element={<ControlDevices me={me} />} /><Route path="/enroll" element={<ControlEnrollment />} /><Route path="/identities" element={<IdentitySettings me={me} providers={providers} />} /><Route path="/settings/domains" element={<DomainSettings />} /><Route path="*" element={<ControlDevices me={me} />} /></Routes><Feedback error={error} /></main></div>;
}

function ControlDevices({ me }: { me: Me }) {
  const [devices, setDevices] = useState<DeviceView[]>([]); const [loading, setLoading] = useState(true); const [error, setError] = useState("");
  const load = useCallback(async () => { setLoading(true); setError(""); try { setDevices((await api<{ devices: DeviceView[] }>("/api/v1/devices")).devices); } catch (reason) { setError(errorMessage(reason)); } finally { setLoading(false); } }, []);
  useEffect(() => { void load(); }, [load]);
  return <section className="page"><div className="summary-line"><div><h1>设备</h1><p>{me.name || me.email}</p></div><button className="icon-button" onClick={() => void load()} title="刷新" aria-label="刷新"><RefreshCw size={16} /></button></div>{loading ? <CenteredLoader label="读取设备" compact /> : devices.length === 0 ? <EmptyState icon={<Unplug size={24} />} label="暂无设备" /> : <div className="device-list">{devices.map((device) => <article className="device-row" key={device.id}><span className="device-icon"><HardDrive size={19} /></span><div className="device-copy"><strong>{device.name}</strong><small>{device.status?.tailnet_ip || device.agent_url}</small></div><StatusPill label={device.status?.online ? "在线" : "离线"} tone={device.status?.online ? "good" : "muted"} /><StatusPill label={device.status?.connection || "未连接"} tone={connectionTone(device.status?.connection)} /><a className="icon-button" href={`/devices/${encodeURIComponent(device.id)}/open`} title="打开设备" aria-label={`打开 ${device.name}`}><ExternalLink size={17} /></a></article>)}</div>}<Feedback error={error} /></section>;
}

function ControlEnrollment() {
  async function create(policy: EnrollmentPolicy) {
    const result = await api<{ join_info: string; expires_at: string }>("/api/v1/device-enrollments", { method: "POST", body: JSON.stringify(policy) });
    return { command: `homestack-agent enroll --descriptor '${result.join_info}' --name '${shellQuote(policy.device_name)}' --agent-url '${shellQuote(policy.agent_url)}'`, expires_at: result.expires_at };
  }
  return <section className="page"><h1>配对 Linux 设备</h1><EnrollmentForm create={create} /></section>;
}

function IdentitySettings({ me, providers }: { me: Me; providers: Provider[] }) {
  const linked = new Set(me.identities.map((identity) => identity.provider));
  return <section className="page"><h1>登录身份</h1><div className="settings-list">{providers.map((provider) => <div key={provider.id}><span>{provider.label}</span>{linked.has(provider.id) ? <strong>已绑定</strong> : <a href={`/auth/link/${encodeURIComponent(provider.id)}`}>绑定</a>}</div>)}</div></section>;
}

interface SystemConfiguration { control_host: string; pocket_host: string; mesh_host: string; tail_host: string; public_ipv4: string }
interface ReconfigureStatus { phase: "idle" | "preflight" | "applying" | "rollback" | "completed" | "failed"; target_url?: string; error?: string }

function DomainSettings() {
  const [config, setConfig] = useState<SystemConfiguration | null>(null);
  const [confirmation, setConfirmation] = useState("");
  const [status, setStatus] = useState<ReconfigureStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const reauthenticated = new URLSearchParams(location.search).get("reauthenticated") === "1";
  useEffect(() => {
    Promise.all([api<SystemConfiguration>("/api/v1/system/config"), api<ReconfigureStatus>("/api/v1/system/reconfigure/status")])
      .then(([current, migration]) => { setConfig(current); setStatus(migration); })
      .catch((reason) => setError(errorMessage(reason)));
  }, []);
  function field(name: keyof SystemConfiguration, label: string) {
    return <label>{label}<input value={config?.[name] || ""} onChange={(event) => setConfig(config ? { ...config, [name]: event.target.value } : config)} required /></label>;
  }
  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!config) return;
    setBusy(true); setError("");
    try {
      const next = await api<ReconfigureStatus>("/api/v1/system/reconfigure", { method: "POST", body: JSON.stringify({ ...config, confirmation }) });
      setStatus(next);
      await pollMigration();
    } catch (reason) {
      setError(errorMessage(reason)); setBusy(false);
    }
  }
  async function pollMigration() {
    for (let attempt = 0; attempt < 160; attempt += 1) {
      try {
        const next = await api<ReconfigureStatus>("/api/v1/system/reconfigure/status");
        setStatus(next);
        if (next.phase === "completed" && next.target_url) { location.assign(next.target_url); return; }
        if (next.phase === "failed") { setError(next.error || "域名迁移失败"); setBusy(false); return; }
      } catch { /* Control 重启期间连接会短暂中断。 */ }
      await new Promise((resolve) => window.setTimeout(resolve, 1500));
    }
    setError("域名迁移未在预期时间内完成"); setBusy(false);
  }
  if (!config) return <CenteredLoader label="读取系统配置" compact />;
  const active = status && ["preflight", "applying", "rollback"].includes(status.phase);
  return <section className="page domain-settings"><div className="summary-line"><div><h1>域名与网络</h1><p>{status?.phase === "failed" ? "迁移失败" : active ? "迁移进行中" : "配置已就绪"}</p></div>{active ? <LoaderCircle className="spin" size={18} /> : <Check size={18} />}</div><form className="form-grid" onSubmit={(event) => void submit(event)}>{field("control_host", "Control 域名")}{field("pocket_host", "Pocket ID 域名")}{field("mesh_host", "Headscale 域名")}{field("tail_host", "Tailnet 基础域名")}{field("public_ipv4", "VPS 公网 IPv4")}<div className="maintenance-confirm"><a className="secondary-button" href="/auth/reauth/pocket?return=/settings/domains"><ShieldCheck size={16} />{reauthenticated ? "已重新认证" : "使用 Passkey 重新认证"}</a><label>输入新的 Control 域名确认<input value={confirmation} onChange={(event) => setConfirmation(event.target.value)} required /></label></div><button className="primary-button" disabled={busy || Boolean(active) || !reauthenticated || confirmation !== config.control_host}>{busy || active ? <LoaderCircle className="spin" size={16} /> : <Settings size={16} />}应用配置</button></form><Feedback error={error || status?.error} /></section>;
}

function shellQuote(value: string) { return value.replaceAll("'", `'"'"'`); }
