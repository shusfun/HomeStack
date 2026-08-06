import { Clipboard, LoaderCircle } from "lucide-react";
import { FormEvent, useState } from "react";
import { Feedback, errorMessage } from "./ui";

export interface EnrollmentPolicy {
  device_name: string;
  agent_url: string;
  modules: Array<Record<string, unknown>>;
  shared_directories: Array<Record<string, unknown>>;
  module_secrets: Record<string, Record<string, string>>;
}

export function EnrollmentForm({ create }: { create: (policy: EnrollmentPolicy) => Promise<{ command: string; expires_at: string }> }) {
  const [name, setName] = useState("");
  const [agentURL, setAgentURL] = useState("");
  const [fileToken, setFileToken] = useState("");
  const [jellyfinKey, setJellyfinKey] = useState("");
  const [command, setCommand] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setCommand("");
    const modules: Array<Record<string, unknown>> = [];
    const secrets: Record<string, Record<string, string>> = {};
    if (fileToken) {
      modules.push({ id: "filebrowser", enabled: true, base_url: "http://127.0.0.1:8080", read_only: true });
      secrets.filebrowser = { api_token: fileToken };
    }
    if (jellyfinKey) {
      modules.push({ id: "jellyfin", enabled: true, base_url: "http://127.0.0.1:8096", read_only: true });
      secrets.jellyfin = { api_key: jellyfinKey };
    }
    try {
      const result = await create({
        device_name: name, agent_url: agentURL, modules,
        shared_directories: fileToken ? [{ id: "default", name: "文件", permissions: ["read", "download"] }] : [],
        module_secrets: secrets,
      });
      setCommand(result.command); setExpiresAt(result.expires_at);
    } catch (reason) { setError(errorMessage(reason)); } finally { setBusy(false); }
  }

  async function copy() { await navigator.clipboard.writeText(command); }

  return <>
    <form className="form-grid" onSubmit={submit}>
      <label><span>设备名称</span><input value={name} onChange={(event) => setName(event.target.value)} required /></label>
      <label><span>Agent HTTPS 地址</span><input type="url" value={agentURL} onChange={(event) => setAgentURL(event.target.value)} placeholder="https://nas.example.ts.net:9443" required /></label>
      <label><span>FileBrowser API Token</span><input type="password" value={fileToken} onChange={(event) => setFileToken(event.target.value)} /></label>
      <label><span>Jellyfin API Key</span><input type="password" value={jellyfinKey} onChange={(event) => setJellyfinKey(event.target.value)} /></label>
      <button className="primary-button" disabled={busy}>{busy ? <LoaderCircle className="spin" size={17} /> : null}{busy ? "生成中" : "生成配对命令"}</button>
    </form>
    {command && <div className="command-box"><div><code>{command}</code><small>有效至 {new Date(expiresAt).toLocaleString("zh-CN")}</small></div><button className="icon-button" onClick={() => void copy()} title="复制命令" aria-label="复制命令"><Clipboard size={17} /></button></div>}
    <Feedback error={error} />
  </>;
}
