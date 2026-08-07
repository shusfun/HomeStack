import { Check, Clipboard, ExternalLink, LoaderCircle, LockKeyhole, ServerCog } from "lucide-react";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { api } from "./api";
import { BrandMark } from "./BrandMark";
import { Feedback } from "./ui";

type Phase = "token" | "infrastructure" | "pocket-id" | "finalize" | "completed";
interface SetupConfiguration { control_host: string; pocket_host: string; mesh_host: string; tail_host: string; public_ipv4: string }
interface SetupStatus { surface: "setup"; phase: Phase; config?: SetupConfiguration; pocket_url?: string; error?: string }

const emptyConfiguration: SetupConfiguration = { control_host: "", pocket_host: "", mesh_host: "", tail_host: "", public_ipv4: "" };

export function SetupApp() {
  const [status, setStatus] = useState<SetupStatus>({ surface: "setup", phase: "token" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const load = useCallback(async () => setStatus(await api<SetupStatus>("/api/v1/setup/status")), []);
  useEffect(() => { void load(); }, [load]);
  if (status.phase === "token") return <TokenStep onReady={load} busy={busy} setBusy={setBusy} error={error} setError={setError} />;
  return <SetupShell phase={status.phase}>
    {status.phase === "infrastructure" && <InfrastructureStep initial={status.config} onPrepared={setStatus} busy={busy} setBusy={setBusy} error={error} setError={setError} />}
    {status.phase === "pocket-id" && <PocketStep status={status} onStatus={setStatus} refresh={load} busy={busy} setBusy={setBusy} error={error} setError={setError} />}
    {status.phase === "finalize" && <FinalizingStep />}
    {status.phase === "completed" && <CompletedStep />}
  </SetupShell>;
}

function SetupShell({ phase, children }: { phase: Phase; children: React.ReactNode }) {
  const steps: Phase[] = ["infrastructure", "pocket-id", "finalize", "completed"];
  const active = Math.max(0, steps.indexOf(phase));
  return <main className="setup-shell"><header><BrandMark className="setup-mark" /><div><strong>HomeStack</strong><span>Setup</span></div></header><div className="setup-progress" aria-label="初始化进度">{steps.map((step, index) => <span key={step} className={index <= active ? "active" : ""}>{index < active ? <Check size={14} /> : index + 1}</span>)}</div>{children}</main>;
}

function TokenStep({ onReady, busy, setBusy, error, setError }: StepProps & { onReady: () => Promise<void> }) {
  const [token, setToken] = useState(() => new URLSearchParams(location.hash.slice(1)).get("token") || "");
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError("");
    try { await api<void>("/api/v1/setup/session", { method: "POST", body: JSON.stringify({ token }) }); history.replaceState(null, "", location.pathname); await onReady(); }
    catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); }
  }
  return <main className="setup-token"><BrandMark className="setup-token-mark" /><h1>HomeStack Setup</h1><form onSubmit={(event) => void submit(event)}><label>一次性令牌<input type="password" autoComplete="one-time-code" value={token} onChange={(event) => setToken(event.target.value)} required /></label><button className="primary-button" disabled={busy || !token}>{busy ? <LoaderCircle className="spin" size={16} /> : <LockKeyhole size={16} />}进入初始化</button></form><Feedback error={error} /></main>;
}

function InfrastructureStep({ initial, onPrepared, busy, setBusy, error, setError }: StepProps & { initial?: SetupConfiguration; onPrepared: (status: SetupStatus) => void }) {
  const [config, setConfig] = useState(initial || emptyConfiguration);
  function field(name: keyof SetupConfiguration, label: string, placeholder: string) {
    return <label>{label}<input value={config[name]} placeholder={placeholder} onChange={(event) => setConfig({ ...config, [name]: event.target.value })} required /></label>;
  }
  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError("");
    try { onPrepared(await api<SetupStatus>("/api/v1/setup/prepare", { method: "POST", body: JSON.stringify(config) })); }
    catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); }
  }
  return <section className="setup-panel"><div className="setup-heading"><ServerCog size={22} /><div><h1>基础设施</h1><p>填写已直连当前 VPS 的域名</p></div></div><form className="setup-form" onSubmit={(event) => void submit(event)}>{field("control_host", "Control 域名", "app.example.com")}{field("pocket_host", "Pocket ID 域名", "id.example.com")}{field("mesh_host", "Headscale 域名", "mesh.example.com")}{field("tail_host", "Tailnet 基础域名", "tail.example.com")}{field("public_ipv4", "VPS 公网 IPv4", "203.0.113.10")}<button className="primary-button" disabled={busy}>{busy ? <LoaderCircle className="spin" size={16} /> : <ServerCog size={16} />}安装并配置</button></form><Feedback error={error} /></section>;
}

function PocketStep({ status, onStatus, refresh, busy, setBusy, error, setError }: StepProps & { status: SetupStatus; onStatus: (status: SetupStatus) => void; refresh: () => Promise<void> }) {
  async function finalize() {
    setBusy(true); setError("");
    try { setStatusAndPoll(await api<SetupStatus>("/api/v1/setup/finalize", { method: "POST", body: "{}" })); }
    catch (reason) { setError(errorMessage(reason)); setBusy(false); }
  }
  function setStatusAndPoll(next: SetupStatus) {
    onStatus(next);
    if (next.phase === "finalize") window.setTimeout(() => void waitForControl(), 1500);
    else void refresh().finally(() => setBusy(false));
  }
  async function waitForControl() {
    for (let attempt = 0; attempt < 40; attempt += 1) {
      try {
        const response = await fetch("/api/v1/meta", { credentials: "same-origin" });
        if (response.ok && response.headers.get("content-type")?.includes("application/json")) { location.assign("/"); return; }
        const setupStatus = await api<SetupStatus>("/api/v1/setup/status");
        if (setupStatus.phase === "pocket-id") {
          onStatus(setupStatus); setError(setupStatus.error || "正式服务切换失败"); setBusy(false); return;
        }
      } catch { /* 服务切换期间连接会短暂中断。 */ }
      await new Promise((resolve) => window.setTimeout(resolve, 1500));
    }
    setError("Control 未在预期时间内启动，请检查服务日志"); setBusy(false);
  }
  const pocketURL = status.pocket_url || (status.config ? `https://${status.config.pocket_host}/setup` : "#");
  return <section className="setup-panel"><div className="setup-heading"><LockKeyhole size={22} /><div><h1>Pocket ID</h1><p>创建首个 Passkey 管理员后继续</p></div></div><div className="setup-actions"><a className="primary-button" href={pocketURL} target="_blank" rel="noreferrer"><ExternalLink size={16} />打开 Pocket ID</a><button className="secondary-button" onClick={() => void copy(pocketURL)}><Clipboard size={16} />复制地址</button></div><button className="primary-button setup-finish" disabled={busy} onClick={() => void finalize()}>{busy ? <LoaderCircle className="spin" size={16} /> : <Check size={16} />}完成初始化</button><Feedback error={error || status.error} /></section>;
}

function FinalizingStep() { return <section className="setup-panel setup-center"><LoaderCircle className="spin" size={26} /><h1>正在启动 HomeStack</h1></section>; }
function CompletedStep() { return <section className="setup-panel setup-center"><Check size={28} /><h1>初始化已完成</h1><a className="primary-button" href="/">进入 HomeStack</a></section>; }

interface StepProps { busy: boolean; setBusy: (value: boolean) => void; error: string; setError: (value: string) => void }
function errorMessage(reason: unknown) { return reason instanceof Error ? reason.message : String(reason); }
async function copy(value: string) { await navigator.clipboard.writeText(value); }
