import { Activity, ChevronLeft, Download, File, Film, Folder, FolderOpen, Gauge, HardDrive, Network, RefreshCw, ScrollText, Server, Settings, Wrench } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { NavLink, Route, Routes } from "react-router-dom";
import { api, formatBytes, formatTime } from "./api";
import { BrandMark } from "./BrandMark";
import type { DeviceStatus, FileItem, FileResource, MediaItem } from "./types";
import { CenteredLoader, EmptyState, Feedback, StatusPill, connectionTone, errorMessage } from "./ui";

interface Metrics { cpu_percent: number; memory_used: number; memory_total: number; disk_used: number; disk_total: number; network_rx: number; network_tx: number; load_one_minute: number }
interface ServiceStatus { id: string; state: string; detail?: string; actions: string[] }
interface LogEntry { cursor: string; timestamp: string; service: string; message: string }
interface AgentUpdateStatus {
  state: string; current_version: string; latest_version?: string; published_at?: string; notes?: string;
  downloaded?: number; total?: number; signature: string; error?: string;
}

const nav = [
  { to: "/", label: "概览", icon: Gauge }, { to: "/files", label: "文件", icon: FolderOpen },
  { to: "/media", label: "影视", icon: Film }, { to: "/services", label: "服务", icon: Wrench },
  { to: "/logs", label: "日志", icon: ScrollText }, { to: "/updates", label: "更新", icon: Download },
];

export function AgentApp() {
  return <div className="agent-shell"><aside><div className="agent-brand"><BrandMark className="brand-mark" /><div><strong>HomeStack</strong><small>Agent</small></div></div><nav>{nav.map((item) => <NavLink key={item.to} to={item.to} end={item.to === "/"}><item.icon size={17} /><span>{item.label}</span></NavLink>)}</nav></aside><main><Routes><Route path="/" element={<Overview />} /><Route path="/files" element={<Files />} /><Route path="/media" element={<Media />} /><Route path="/services" element={<Services />} /><Route path="/logs" element={<Logs />} /><Route path="/updates" element={<AgentUpdates />} /><Route path="*" element={<Overview />} /></Routes></main></div>;
}

function Overview() {
  const [status, setStatus] = useState<DeviceStatus | null>(null); const [metrics, setMetrics] = useState<Metrics | null>(null); const [error, setError] = useState("");
  const load = useCallback(async () => { setError(""); try { const [device, system] = await Promise.all([api<DeviceStatus>("/api/status"), api<Metrics>("/api/system/metrics")]); setStatus(device); setMetrics(system); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), 10_000); return () => window.clearInterval(timer); }, [load]);
  return <AgentPage title={status?.name || "设备概览"} action={<button className="icon-button" onClick={() => void load()} title="刷新" aria-label="刷新"><RefreshCw size={16} /></button>}>{metrics && <div className="metric-grid"><Metric icon={<Activity size={17} />} label="CPU" value={`${metrics.cpu_percent.toFixed(1)}%`} /><Metric icon={<Gauge size={17} />} label="内存" value={`${formatBytes(metrics.memory_used)} / ${formatBytes(metrics.memory_total)}`} /><Metric icon={<HardDrive size={17} />} label="磁盘" value={`${formatBytes(metrics.disk_used)} / ${formatBytes(metrics.disk_total)}`} /><Metric icon={<Network size={17} />} label="网络累计" value={`↓ ${formatBytes(metrics.network_rx)}  ↑ ${formatBytes(metrics.network_tx)}`} /></div>}{status && <><div className="status-line"><StatusPill label={status.online ? "在线" : "离线"} tone={status.online ? "good" : "muted"} /><StatusPill label={status.connection} tone={connectionTone(status.connection)} /><span>{status.tailscale_ip}</span><span>配置 r{status.config_revision}</span></div><div className="data-table"><div className="table-head"><span>模块</span><span>状态</span><span>版本</span></div>{status.modules.map((module) => <div className="table-row" key={module.id}><strong>{module.id}</strong><StatusPill label={module.state} tone={module.state === "ready" ? "good" : "warn"} /><span>{module.version || module.expected_version}</span></div>)}</div></>}<Feedback error={error} /></AgentPage>;
}

function Files() {
  const [path, setPath] = useState("/"); const [resource, setResource] = useState<FileResource | null>(null); const [error, setError] = useState("");
  const load = useCallback(async (target: string) => { setError(""); try { setResource(await api<FileResource>(`/api/files/resources?path=${encodeURIComponent(target)}`)); setPath(target); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load("/"); }, [load]);
  const parent = useMemo(() => path === "/" ? "/" : path.split("/").slice(0, -1).join("/") || "/", [path]);
  const child = (name: string) => `${path === "/" ? "" : path}/${name}`;
  function activate(item: FileItem) { if (item.type === "directory") void load(child(item.name)); else location.assign(`/api/files/raw?files=${encodeURIComponent(child(item.name))}`); }
  return <AgentPage title="文件" action={<button className="icon-button" onClick={() => void load(path)} title="刷新" aria-label="刷新"><RefreshCw size={16} /></button>}><div className="pathbar"><button className="icon-button" disabled={path === "/"} onClick={() => void load(parent)} title="上一级"><ChevronLeft size={17} /></button><code>{path}</code></div><div className="file-table">{[...(resource?.folders || []), ...(resource?.files || [])].map((item) => <button className="file-row" key={`${item.type}-${item.name}`} onClick={() => activate(item)}><span>{item.type === "directory" ? <Folder size={17} /> : <File size={17} />}<strong>{item.name}</strong></span><span>{item.type === "directory" ? "文件夹" : formatBytes(item.size)}</span><span>{formatTime(item.modified)}</span></button>)}</div>{!resource && !error && <CenteredLoader label="读取文件" compact />}<Feedback error={error} /></AgentPage>;
}

function Media() {
  const [items, setItems] = useState<MediaItem[]>([]); const [selected, setSelected] = useState<{ item: MediaItem; playSessionId: string } | null>(null); const [error, setError] = useState("");
  const lastProgress = useRef(0);
  const load = useCallback(async () => { setError(""); try { setItems((await api<{ Items: MediaItem[] }>("/api/media/Items?Recursive=true&IncludeItemTypes=Movie,Episode&Fields=ProductionYear,RunTimeTicks,SeriesName&Limit=100")).Items || []); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load(); }, [load]);
  async function select(item: MediaItem) { setError(""); try { const playback = await api<{ PlaySessionId?: string }>(`/api/media/Items/${encodeURIComponent(item.Id)}/PlaybackInfo`, { method: "POST", body: "{}" }); if (!playback.PlaySessionId) throw new Error("Jellyfin 未返回播放会话"); lastProgress.current = 0; setSelected({ item, playSessionId: playback.PlaySessionId }); } catch (reason) { setError(errorMessage(reason)); } }
  async function report(path: "Playing" | "Playing/Progress" | "Playing/Stopped", video: HTMLVideoElement) { if (!selected) return; try { await api<void>(`/api/media/Sessions/${path}`, { method: "POST", body: JSON.stringify({ ItemId: selected.item.Id, PlaySessionId: selected.playSessionId, PositionTicks: Math.floor(video.currentTime * 10_000_000), IsPaused: video.paused, IsMuted: video.muted, VolumeLevel: Math.round(video.volume * 100), PlayMethod: "Transcode" }) }); } catch (reason) { setError(errorMessage(reason)); } }
  function progress(video: HTMLVideoElement) { if (video.currentTime - lastProgress.current < 10) return; lastProgress.current = video.currentTime; void report("Playing/Progress", video); }
  return <AgentPage title="影视" action={<button className="icon-button" onClick={() => void load()} title="刷新" aria-label="刷新"><RefreshCw size={16} /></button>}>{selected && <section className="player"><video controls autoPlay src={`/api/media/Videos/${encodeURIComponent(selected.item.Id)}/stream.mp4`} onPlay={(event) => void report("Playing", event.currentTarget)} onPause={(event) => void report("Playing/Progress", event.currentTarget)} onTimeUpdate={(event) => progress(event.currentTarget)} onEnded={(event) => void report("Playing/Stopped", event.currentTarget)} /><div><strong>{selected.item.Name}</strong><button onClick={() => setSelected(null)}>关闭</button></div></section>}<div className="media-grid">{items.map((item) => <button key={item.Id} onClick={() => void select(item)}><img src={`/api/media/Items/${encodeURIComponent(item.Id)}/Images/Primary?maxWidth=480&quality=85`} alt="" loading="lazy" /><span><strong>{item.Name}</strong><small>{item.SeriesName || item.ProductionYear || item.Type}</small></span></button>)}</div>{items.length === 0 && !error && <EmptyState icon={<Film size={24} />} label="媒体库为空" />}<Feedback error={error} /></AgentPage>;
}

function Services() {
  const [services, setServices] = useState<ServiceStatus[]>([]); const [error, setError] = useState("");
  const load = useCallback(async () => { setError(""); try { setServices((await api<{ services: ServiceStatus[] }>("/api/services")).services); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load(); }, [load]);
  async function action(id: string, value: string) { setError(""); try { await api<void>(`/api/services/${encodeURIComponent(id)}/actions`, { method: "POST", body: JSON.stringify({ action: value }) }); await load(); } catch (reason) { setError(errorMessage(reason)); } }
  return <AgentPage title="服务"><div className="service-list">{services.map((item) => <div className="service-row" key={item.id}><div><strong>{item.id}</strong><small>{item.detail}</small></div><StatusPill label={item.state} tone={item.state === "active" ? "good" : "warn"} /><div>{item.actions.map((value) => <button key={value} onClick={() => void action(item.id, value)}>{actionLabel(value)}</button>)}</div></div>)}</div><Feedback error={error} /></AgentPage>;
}

function Logs() {
  const [service, setService] = useState("homestack-agent"); const [entries, setEntries] = useState<LogEntry[]>([]); const [cursor, setCursor] = useState(""); const [error, setError] = useState("");
  const load = useCallback(async (after = "") => { setError(""); try { const page = await api<{ entries: LogEntry[]; next_cursor?: string }>(`/api/logs?service=${encodeURIComponent(service)}&limit=200${after ? `&cursor=${encodeURIComponent(after)}` : ""}`); setEntries(after ? (current) => [...current, ...page.entries] : page.entries); setCursor(page.next_cursor || ""); } catch (reason) { setError(errorMessage(reason)); } }, [service]);
  useEffect(() => { void load(); }, [load]);
  return <AgentPage title="日志" action={<select value={service} onChange={(event) => setService(event.target.value)}><option value="homestack-agent">Agent</option><option value="tailscale">Tailscale</option><option value="jellyfin">Jellyfin</option></select>}><div className="log-view">{entries.map((entry) => <div key={entry.cursor}><time>{formatTime(entry.timestamp)}</time><code>{entry.message}</code></div>)}</div>{cursor && <button className="secondary-button" onClick={() => void load(cursor)}>加载后续</button>}<Feedback error={error} /></AgentPage>;
}

function AgentUpdates() {
  const [status, setStatus] = useState<AgentUpdateStatus | null>(null); const [busy, setBusy] = useState(false); const [error, setError] = useState("");
  const load = useCallback(async () => { setStatus(await api<AgentUpdateStatus>("/api/updates/status")); }, []);
  const check = useCallback(async () => { setError(""); try { setStatus(await api<AgentUpdateStatus>("/api/updates/check", { method: "POST" })); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load().catch((reason) => setError(errorMessage(reason))); }, [load]);
  async function download() {
    setBusy(true); setError("");
    const operation = api<AgentUpdateStatus>("/api/updates/download", { method: "POST" });
    const timer = window.setInterval(() => { void load().catch((reason) => setError(errorMessage(reason))); }, 250);
    try { setStatus(await operation); } catch (reason) { setError(errorMessage(reason)); await load(); }
    finally { window.clearInterval(timer); setBusy(false); }
  }
  const percent = status?.total ? Math.round(((status.downloaded || 0) / status.total) * 100) : 0;
  return <AgentPage title="Agent 更新"><div className="update-grid"><span>当前版本</span><strong>{status?.current_version || "-"}</strong><span>最新版本</span><strong>{status?.latest_version || "-"}</strong><span>状态</span><strong>{status?.state || "读取中"}</strong><span>签名</span><strong>{status?.signature || "-"}</strong></div>{status?.published_at && <p className="muted">发布于 {new Date(status.published_at).toLocaleString("zh-CN")}</p>}{status?.notes && <pre className="release-notes">{status.notes}</pre>}{status?.state === "downloading" && <div className="progress"><span style={{ width: `${percent}%` }} /></div>}<div className="button-row"><button className="secondary-button" disabled={busy} onClick={() => void check()}><RefreshCw size={16} />检查更新</button>{status?.state === "available" && <button className="primary-button" disabled={busy} onClick={() => void download()}><Download size={16} />下载并校验</button>}{status?.state === "ready" && <button className="primary-button" onClick={() => void api<AgentUpdateStatus>("/api/updates/install", { method: "POST" }).then(setStatus).catch((reason) => setError(errorMessage(reason)))}><Settings size={16} />安装并重启</button>}</div><Feedback error={error || status?.error} /></AgentPage>;
}

function AgentPage({ title, action, children }: { title: string; action?: React.ReactNode; children: React.ReactNode }) { return <section className="agent-page"><header><h1>{title}</h1>{action}</header>{children}</section>; }
function Metric({ icon, label, value }: { icon: React.ReactNode; label: string; value: string }) { return <div className="metric"><span>{icon}</span><small>{label}</small><strong>{value}</strong></div>; }
function actionLabel(value: string) { return ({ start: "启动", stop: "停止", restart: "重启" } as Record<string, string>)[value] || value; }
