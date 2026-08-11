import { Check, LoaderCircle, LockKeyhole, ServerCog } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useState, type ReactNode } from "react";
import { api } from "./api";
import { BrandMark } from "./BrandMark";
import { AuthLayout, Button, InlineNotice, Input, Loading, PasswordInput, SegmentedControl, errorMessage } from "./components/ui";
import { browserPublicHost, changeSetupProvider, oauthCallback, type OAuthProviderID, type SetupFormValue } from "./publicAddress";

type Phase = "token" | "domain" | "identity" | "finalize" | "completed";
interface PublicProvider { id: OAuthProviderID; client_id: string }
interface PublicConfiguration { public_host: string; providers: PublicProvider[] }
interface SetupStatus { surface: "setup"; phase: Phase; config?: PublicConfiguration; error?: string }

function initialConfiguration(initial?: PublicConfiguration): SetupFormValue {
  const provider = initial?.providers[0];
  return { public_host: initial?.public_host || browserPublicHost(window.location), provider: provider?.id || "google", client_id: provider?.client_id || "", client_secret: "" };
}

export function SetupApp() {
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(async () => { setError(""); try { setStatus(await api<SetupStatus>("/api/setup/status")); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load(); }, [load]);
  if (!status && error) return <AuthLayout title="无法读取初始化状态"><InlineNotice tone="danger">{error}</InlineNotice><Button onClick={() => void load()}><LoaderCircle size={16} />重试</Button></AuthLayout>;
  if (!status) return <Loading label="正在读取初始化状态" />;
  if (status.phase === "token") return <TokenStep onReady={load} busy={busy} setBusy={setBusy} error={error} setError={setError} />;
  return <SetupShell phase={status.phase}>
    {status.phase === "domain" && <ConfigurationStep initial={status.config} onPrepared={setStatus} busy={busy} setBusy={setBusy} error={error} setError={setError} />}
    {status.phase === "identity" && <ReadyStep status={status} onStatus={setStatus} busy={busy} setBusy={setBusy} error={error} setError={setError} />}
    {status.phase === "finalize" && <FinalizingStep />}
    {status.phase === "completed" && <CompletedStep />}
  </SetupShell>;
}

function SetupShell({ phase, children }: { phase: Phase; children: ReactNode }) {
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
  return <AuthLayout title="HomeStack Setup" description="输入服务器生成的一次性令牌开始初始化。"><form className="setup-token-form" onSubmit={(event) => void submit(event)}><label>一次性令牌<PasswordInput autoComplete="one-time-code" value={token} onChange={(event) => setToken(event.target.value)} required /></label><Button disabled={busy || !token} loading={busy} type="submit"><LockKeyhole size={16} />进入初始化</Button></form>{error && <InlineNotice tone="danger">{error}</InlineNotice>}</AuthLayout>;
}

function ConfigurationStep({ initial, onPrepared, busy, setBusy, error, setError }: StepProps & { initial?: PublicConfiguration; onPrepared: (status: SetupStatus) => void }) {
  const [config, setConfig] = useState<SetupFormValue>(() => initialConfiguration(initial));
  const callback = oauthCallback(config.public_host, config.provider);
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError("");
    try { onPrepared(await api<SetupStatus>("/api/setup/prepare", { method: "POST", body: JSON.stringify(config) })); }
    catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); }
  }
  function selectProvider(provider: OAuthProviderID) { setError(""); setConfig((current) => changeSetupProvider(current, provider)); }
  return <section className="setup-panel"><div className="setup-heading"><ServerCog size={22} /><div><h1>VPS 与登录</h1><p>配置公网地址和一种登录方式</p></div></div><form className="setup-form" onSubmit={(event) => void submit(event)}><label>VPS 地址<Input value={config.public_host} placeholder="hs.waasabi.cloud" onChange={(event) => setConfig({ ...config, public_host: event.target.value })} required /></label><fieldset><legend>登录方式</legend><SegmentedControl ariaLabel="登录方式" onChange={(value) => selectProvider(value as OAuthProviderID)} options={[{ value: "google", label: "Google" }, { value: "github", label: "GitHub" }]} value={config.provider} /></fieldset><label>OAuth Client ID<Input value={config.client_id} onChange={(event) => setConfig({ ...config, client_id: event.target.value })} required /></label><label>OAuth Client Secret<PasswordInput autoComplete="new-password" value={config.client_secret} onChange={(event) => setConfig({ ...config, client_secret: event.target.value })} required /></label>{callback && <label>OAuth 回调地址<output key={config.provider}>{callback}</output></label>}<Button disabled={busy || !callback} loading={busy} type="submit"><ServerCog size={16} />校验并保存</Button></form>{error && <InlineNotice tone="danger">{error}</InlineNotice>}</section>;
}

function ReadyStep({ status, onStatus, busy, setBusy, error, setError }: StepProps & { status: SetupStatus; onStatus: (status: SetupStatus) => void }) {
  async function finalize() {
    setBusy(true); setError("");
    try {
      const next = await api<SetupStatus>("/api/setup/finalize", { method: "POST", body: "{}" });
      onStatus(next);
      for (let attempt = 0; attempt < 40; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 1500));
        try { const response = await fetch("/api/meta", { credentials: "same-origin" }); if (response.ok) { location.assign("/login"); return; } } catch { /* unit 切换期间连接会短暂中断。 */ }
      }
      throw new Error("Control 未在预期时间内启动，请检查服务日志");
    } catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  }
  const provider = status.config?.providers[0]?.id;
  return <section className="setup-panel setup-center"><Check size={28} /><h1>配置已校验</h1><p>{status.config?.public_host} 将使用 {provider === "google" ? "Google" : "GitHub"} 登录</p><Button className="setup-finish" disabled={busy} loading={busy} onClick={() => void finalize()}><Check size={16} />启动 HomeStack</Button>{(error || status.error) && <InlineNotice tone="danger">{error || status.error}</InlineNotice>}</section>;
}

function FinalizingStep() { return <section className="setup-panel setup-center"><LoaderCircle className="spin" size={26} /><h1>正在启动 HomeStack</h1></section>; }
function CompletedStep() { return <section className="setup-panel setup-center"><Check size={28} /><h1>初始化已完成</h1><Button asChild><a href="/">进入 HomeStack</a></Button></section>; }
interface StepProps { busy: boolean; setBusy: (value: boolean) => void; error: string; setError: (value: string) => void }
