import { Check, LoaderCircle, LockKeyhole, ServerCog } from "lucide-react";
import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { api } from "./api";
import { BrandMark } from "./BrandMark";
import { Feedback } from "./ui";

type Phase = "token" | "domain" | "identity" | "finalize" | "completed";
type Provider = "google" | "github";
interface SetupConfiguration { public_host: string; provider: Provider; client_id: string; client_secret?: string }
interface SetupStatus { surface: "setup"; phase: Phase; config?: Omit<SetupConfiguration, "client_secret">; error?: string }

const emptyConfiguration: SetupConfiguration = { public_host: "", provider: "google", client_id: "", client_secret: "" };

export function SetupApp() {
  const [status, setStatus] = useState<SetupStatus>({ surface: "setup", phase: "token" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(async () => setStatus(await api<SetupStatus>("/api/setup/status")), []);
  useEffect(() => { void load(); }, [load]);
  if (status.phase === "token") return <TokenStep onReady={load} busy={busy} setBusy={setBusy} error={error} setError={setError} />;
  return <SetupShell phase={status.phase}>
    {status.phase === "domain" && <ConfigurationStep initial={status.config} onPrepared={setStatus} busy={busy} setBusy={setBusy} error={error} setError={setError} />}
    {status.phase === "identity" && <ReadyStep status={status} onStatus={setStatus} busy={busy} setBusy={setBusy} error={error} setError={setError} />}
    {status.phase === "finalize" && <FinalizingStep />}
    {status.phase === "completed" && <CompletedStep />}
  </SetupShell>;
}

function SetupShell({ phase, children }: { phase: Phase; children: React.ReactNode }) {
  const steps: Phase[] = ["domain", "identity", "finalize", "completed"];
  const active = Math.max(0, steps.indexOf(phase));
  return <main className="setup-shell"><header><BrandMark className="setup-mark" /><div><strong>HomeStack</strong><span>Setup</span></div></header><div className="setup-progress" aria-label="初始化进度">{steps.map((step, index) => <span key={step} className={index <= active ? "active" : ""}>{index < active ? <Check size={14} /> : index + 1}</span>)}</div>{children}</main>;
}

function TokenStep({ onReady, busy, setBusy, error, setError }: StepProps & { onReady: () => Promise<void> }) {
  const [token, setToken] = useState(() => new URLSearchParams(location.hash.slice(1)).get("token") || "");
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError("");
    try { await api<void>("/api/setup/session", { method: "POST", body: JSON.stringify({ token }) }); history.replaceState(null, "", location.pathname); await onReady(); }
    catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); }
  }
  return <main className="setup-token"><BrandMark className="setup-token-mark" /><h1>HomeStack Setup</h1><form onSubmit={(event) => void submit(event)}><label>一次性令牌<input type="password" autoComplete="one-time-code" value={token} onChange={(event) => setToken(event.target.value)} required /></label><button className="primary-button" disabled={busy || !token}>{busy ? <LoaderCircle className="spin" size={16} /> : <LockKeyhole size={16} />}进入初始化</button></form><Feedback error={error} /></main>;
}

function ConfigurationStep({ initial, onPrepared, busy, setBusy, error, setError }: StepProps & { initial?: Omit<SetupConfiguration, "client_secret">; onPrepared: (status: SetupStatus) => void }) {
  const [config, setConfig] = useState<SetupConfiguration>({ ...emptyConfiguration, ...initial });
  const callback = useMemo(() => config.public_host ? `https://${config.public_host}/auth/callback/${config.provider}` : "", [config.public_host, config.provider]);
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError("");
    try { onPrepared(await api<SetupStatus>("/api/setup/prepare", { method: "POST", body: JSON.stringify(config) })); }
    catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); }
  }
  return <section className="setup-panel"><div className="setup-heading"><ServerCog size={22} /><div><h1>VPS 与登录</h1><p>配置唯一公网域名和唯一登录方式</p></div></div><form className="setup-form" onSubmit={(event) => void submit(event)}><label>VPS 域名<input value={config.public_host} placeholder="home.example.com" onChange={(event) => setConfig({ ...config, public_host: event.target.value })} required /></label><label>登录方式<select value={config.provider} onChange={(event) => setConfig({ ...config, provider: event.target.value as Provider })}><option value="google">Google</option><option value="github">GitHub</option></select></label><label>OAuth Client ID<input value={config.client_id} onChange={(event) => setConfig({ ...config, client_id: event.target.value })} required /></label><label>OAuth Client Secret<input type="password" autoComplete="new-password" value={config.client_secret} onChange={(event) => setConfig({ ...config, client_secret: event.target.value })} required /></label>{callback && <label>OAuth 回调地址<output>{callback}</output></label>}<button className="primary-button" disabled={busy}>{busy ? <LoaderCircle className="spin" size={16} /> : <ServerCog size={16} />}校验并保存</button></form><Feedback error={error} /></section>;
}

function ReadyStep({ status, onStatus, busy, setBusy, error, setError }: StepProps & { status: SetupStatus; onStatus: (status: SetupStatus) => void }) {
  async function finalize() {
    setBusy(true); setError("");
    try {
      const next = await api<SetupStatus>("/api/setup/finalize", { method: "POST", body: "{}" });
      onStatus(next);
      for (let attempt = 0; attempt < 40; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 1500));
        try { const response = await fetch("/api/meta", { credentials: "same-origin" }); if (response.ok) { location.assign("/"); return; } } catch { /* unit 切换期间连接会短暂中断。 */ }
      }
      throw new Error("Control 未在预期时间内启动，请检查服务日志");
    } catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  }
  return <section className="setup-panel setup-center"><Check size={28} /><h1>配置已校验</h1><p>{status.config?.public_host} 将使用 {status.config?.provider === "google" ? "Google" : "GitHub"} 登录</p><button className="primary-button setup-finish" disabled={busy} onClick={() => void finalize()}>{busy ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}启动 HomeStack</button><Feedback error={error || status.error} /></section>;
}

function FinalizingStep() { return <section className="setup-panel setup-center"><LoaderCircle className="spin" size={26} /><h1>正在启动 HomeStack</h1></section>; }
function CompletedStep() { return <section className="setup-panel setup-center"><Check size={28} /><h1>初始化已完成</h1><a className="primary-button" href="/">进入 HomeStack</a></section>; }
interface StepProps { busy: boolean; setBusy: (value: boolean) => void; error: string; setError: (value: string) => void }
function errorMessage(reason: unknown) { return reason instanceof Error ? reason.message : String(reason); }
