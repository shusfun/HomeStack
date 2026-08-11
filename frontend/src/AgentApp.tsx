import { Activity, ChevronLeft, Download, Eye, File, Film, Folder, FolderOpen, Gauge, HardDrive, Network, RefreshCw, Search, ScrollText, Settings, Wrench, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { api, formatBytes, formatTime } from "./api";
import type { DeviceStatus, FileItem, FileResource, MediaItem } from "./types";
import { AppShell, Badge, Button, Dialog, EmptyState, IconButton, InlineNotice, Input, Loading, PageHeader, Progress, ScrollArea, Select, errorMessage } from "./components/ui";

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
  return <AppShell nav={nav.map((item) => ({ ...item, end: item.to === "/" }))} product="Agent"><Routes><Route path="/" element={<Overview />} /><Route path="/files" element={<Files />} /><Route path="/media" element={<Media />} /><Route path="/services" element={<Services />} /><Route path="/logs" element={<Logs />} /><Route path="/updates" element={<AgentUpdates />} /><Route path="*" element={<Navigate to="/" replace />} /></Routes></AppShell>;
}

function Overview() {
	const [status, setStatus] = useState<DeviceStatus | null>(null); const [metrics, setMetrics] = useState<Metrics | null>(null); const [statusError, setStatusError] = useState(""); const [metricsError, setMetricsError] = useState("");
	const load = useCallback(async () => {
		setStatusError(""); setMetricsError("");
		const [device, system] = await Promise.allSettled([api<DeviceStatus>("/api/status"), api<Metrics>("/api/system/metrics")]);
		if (device.status === "fulfilled") setStatus(device.value); else setStatusError(errorMessage(device.reason));
		if (system.status === "fulfilled") setMetrics(system.value); else { setMetrics(null); setMetricsError(errorMessage(system.reason)); }
	}, []);
	useEffect(() => { void load(); const timer = window.setInterval(() => void load(), 10_000); return () => window.clearInterval(timer); }, [load]);
	return <AgentPage title={status?.name || "设备概览"} action={<IconButton label="刷新概览" onClick={() => void load()}><RefreshCw size={16} /></IconButton>}>{metrics && <div className="metric-grid"><Metric icon={<Activity size={17} />} label="CPU" value={`${metrics.cpu_percent.toFixed(1)}%`} /><Metric icon={<Gauge size={17} />} label="内存" value={`${formatBytes(metrics.memory_used)} / ${formatBytes(metrics.memory_total)}`} /><Metric icon={<HardDrive size={17} />} label="磁盘" value={`${formatBytes(metrics.disk_used)} / ${formatBytes(metrics.disk_total)}`} /><Metric icon={<Network size={17} />} label="网络累计" value={`↓ ${formatBytes(metrics.network_rx)}  ↑ ${formatBytes(metrics.network_tx)}`} /></div>}{status && <><div className="status-line"><Badge tone={status.online ? "success" : "neutral"}>{status.online ? "在线" : "离线"}</Badge><Badge tone={connectionTone(status.connection)}>{status.connection}</Badge><span>{status.tailscale_ip}</span><span>配置 r{status.config_revision}</span></div><div className="data-table"><div className="table-head"><span>模块</span><span>状态</span><span>版本</span></div>{status.modules.map((module) => <div className="table-row" key={module.id}><strong>{module.id}</strong><Badge tone={module.state === "ready" ? "success" : "warning"}>{module.state}</Badge><span>{module.version || module.expected_version}</span></div>)}</div></>}{statusError && <InlineNotice tone="danger">{statusError}</InlineNotice>}{metricsError && <InlineNotice tone="danger">{metricsError}</InlineNotice>}</AgentPage>;
}

function Files() {
  const [path, setPath] = useState("/"); const [resource, setResource] = useState<FileResource | null>(null); const [results, setResults] = useState<FileItem[] | null>(null); const [query, setQuery] = useState(""); const [preview, setPreview] = useState(""); const [error, setError] = useState("");
  const load = useCallback(async (target: string) => { setError(""); setResults(null); try { setResource(await api<FileResource>(`/api/files/resources?path=${encodeURIComponent(target)}`)); setPath(target); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load("/"); }, [load]);
  const parent = useMemo(() => path === "/" ? "/" : path.split("/").slice(0, -1).join("/") || "/", [path]);
  const child = (name: string) => `${path === "/" ? "" : path}/${name}`;
  const itemPath = (item: FileItem) => item.path || child(item.name);
  function activate(item: FileItem) { if (item.type === "directory") void load(itemPath(item)); else setPreview(itemPath(item)); }
  async function search() { setError(""); if (!query.trim()) { await load(path); return; } try { setResults((await api<{ items: FileItem[] }>(`/api/files/search?q=${encodeURIComponent(query.trim())}&limit=100`)).items); } catch (reason) { setError(errorMessage(reason)); } }
  const items = results || [...(resource?.folders || []), ...(resource?.files || [])];
  return <AgentPage title="文件" action={<IconButton label="刷新文件" onClick={() => void load(path)}><RefreshCw size={16} /></IconButton>}><div className="file-toolbar"><div className="pathbar"><IconButton disabled={path === "/" || results !== null} label="上一级" onClick={() => void load(parent)}><ChevronLeft size={17} /></IconButton><code>{results ? `搜索：${query}` : path}</code></div><div className="file-search"><Input value={query} onChange={(event) => setQuery(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") void search(); }} aria-label="搜索文件" /><IconButton label="搜索" onClick={() => void search()}><Search size={17} /></IconButton></div></div><ScrollArea className="file-table">{items.map((item) => <div className="file-row" key={`${item.type}-${itemPath(item)}`}><Button className="file-entry-button" onClick={() => activate(item)} variant="ghost"><span>{item.type === "directory" ? <Folder size={17} /> : <File size={17} />}<strong>{item.name}</strong></span></Button><span>{item.type === "directory" ? "文件夹" : formatBytes(item.size)}</span><span>{formatTime(item.modified)}</span>{item.type !== "directory" && <div className="file-actions"><IconButton label={`预览 ${item.name}`} onClick={() => setPreview(itemPath(item))}><Eye size={16} /></IconButton><IconButton asChild label={`下载 ${item.name}`}><a href={`/api/files/raw?files=${encodeURIComponent(itemPath(item))}&download=1`}><Download size={16} /></a></IconButton></div>}</div>)}</ScrollArea>{items.length === 0 && !error && <EmptyState icon={<FolderOpen size={24} />} title={results ? "没有匹配文件" : "目录为空"} />}{!resource && !error && <Loading label="读取文件" compact />}{error && <InlineNotice tone="danger">{error}</InlineNotice>}{preview && <FilePreview path={preview} onClose={() => setPreview("")} />}</AgentPage>;
}

function FilePreview({ path, onClose }: { path: string; onClose: () => void }) {
  const source = `/api/files/raw?files=${encodeURIComponent(path)}`; const extension = path.split(".").pop()?.toLowerCase() || "";
  let content: React.ReactNode = <iframe src={source} title={path} />;
  if (["jpg", "jpeg", "png", "gif", "webp", "avif"].includes(extension)) content = <img src={source} alt="" />;
  else if (["mp4", "webm", "mov", "m4v"].includes(extension)) content = <video src={source} controls autoPlay />;
  else if (["mp3", "m4a", "aac", "flac", "wav", "ogg"].includes(extension)) content = <audio src={source} controls autoPlay />;
  return <Dialog onOpenChange={(open) => !open && onClose()} open title={path.split("/").pop() || "文件预览"}><div className="file-preview-content">{content}</div></Dialog>;
}

function Media() {
  const [items, setItems] = useState<MediaItem[]>([]); const [selected, setSelected] = useState<{ item: MediaItem; playSessionId: string } | null>(null); const [error, setError] = useState("");
  const lastProgress = useRef(0);
  const load = useCallback(async () => { setError(""); try { setItems((await api<{ Items: MediaItem[] }>("/api/media/Items?Recursive=true&IncludeItemTypes=Movie,Episode&Fields=ProductionYear,RunTimeTicks,SeriesName&Limit=100")).Items || []); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load(); }, [load]);
  async function select(item: MediaItem) { setError(""); try { const playback = await api<{ PlaySessionId?: string }>(`/api/media/Items/${encodeURIComponent(item.Id)}/PlaybackInfo`, { method: "POST", body: "{}" }); if (!playback.PlaySessionId) throw new Error("Jellyfin 未返回播放会话"); lastProgress.current = 0; setSelected({ item, playSessionId: playback.PlaySessionId }); } catch (reason) { setError(errorMessage(reason)); } }
  async function report(path: "Playing" | "Playing/Progress" | "Playing/Stopped", video: HTMLVideoElement) { if (!selected) return; try { await api<void>(`/api/media/Sessions/${path}`, { method: "POST", body: JSON.stringify({ ItemId: selected.item.Id, PlaySessionId: selected.playSessionId, PositionTicks: Math.floor(video.currentTime * 10_000_000), IsPaused: video.paused, IsMuted: video.muted, VolumeLevel: Math.round(video.volume * 100), PlayMethod: "Transcode" }) }); } catch (reason) { setError(errorMessage(reason)); } }
  function progress(video: HTMLVideoElement) { if (video.currentTime - lastProgress.current < 10) return; lastProgress.current = video.currentTime; void report("Playing/Progress", video); }
  return <AgentPage title="影视" action={<IconButton label="刷新影视" onClick={() => void load()}><RefreshCw size={16} /></IconButton>}>{selected && <section className="player"><video controls autoPlay src={`/api/media/Videos/${encodeURIComponent(selected.item.Id)}/stream.mp4`} onPlay={(event) => void report("Playing", event.currentTarget)} onPause={(event) => void report("Playing/Progress", event.currentTarget)} onTimeUpdate={(event) => progress(event.currentTarget)} onEnded={(event) => void report("Playing/Stopped", event.currentTarget)} /><div><strong>{selected.item.Name}</strong><Button onClick={() => setSelected(null)} tone="default" variant="ghost">关闭</Button></div></section>}<div className="media-grid">{items.map((item) => <Button className="media-item" key={item.Id} onClick={() => void select(item)} tone="default" variant="ghost"><img src={`/api/media/Items/${encodeURIComponent(item.Id)}/Images/Primary?maxWidth=480&quality=85`} alt="" loading="lazy" /><span><strong>{item.Name}</strong><small>{item.SeriesName || item.ProductionYear || item.Type}</small></span></Button>)}</div>{items.length === 0 && !error && <EmptyState icon={<Film size={24} />} title="媒体库为空" />}{error && <InlineNotice tone="danger">{error}</InlineNotice>}</AgentPage>;
}

function Services() {
  const [services, setServices] = useState<ServiceStatus[]>([]); const [error, setError] = useState("");
  const load = useCallback(async () => { setError(""); try { setServices((await api<{ services: ServiceStatus[] }>("/api/services")).services); } catch (reason) { setError(errorMessage(reason)); } }, []);
  useEffect(() => { void load(); }, [load]);
  async function action(id: string, value: string) { setError(""); try { await api<void>(`/api/services/${encodeURIComponent(id)}/actions`, { method: "POST", body: JSON.stringify({ action: value }) }); await load(); } catch (reason) { setError(errorMessage(reason)); } }
  return <AgentPage title="服务"><div className="service-list">{services.map((item) => <div className="service-row" key={item.id}><div><strong>{item.id}</strong><small>{item.detail}</small></div><Badge tone={item.state === "active" ? "success" : "warning"}>{item.state}</Badge><div>{item.actions.map((value) => <Button key={value} onClick={() => void action(item.id, value)} size="sm" tone="default" variant="outline">{actionLabel(value)}</Button>)}</div></div>)}</div>{services.length === 0 && !error && <EmptyState title="暂无服务" />}{error && <InlineNotice tone="danger">{error}</InlineNotice>}</AgentPage>;
}

function Logs() {
  const [service, setService] = useState("homestack-agent"); const [entries, setEntries] = useState<LogEntry[]>([]); const [cursor, setCursor] = useState(""); const [error, setError] = useState("");
  const load = useCallback(async (after = "") => { setError(""); try { const page = await api<{ entries: LogEntry[]; next_cursor?: string }>(`/api/logs?service=${encodeURIComponent(service)}&limit=200${after ? `&cursor=${encodeURIComponent(after)}` : ""}`); setEntries(after ? (current) => [...current, ...page.entries] : page.entries); setCursor(page.next_cursor || ""); } catch (reason) { setError(errorMessage(reason)); } }, [service]);
  useEffect(() => { void load(); }, [load]);
  return <AgentPage title="日志" action={<Select ariaLabel="日志服务" onValueChange={setService} options={[{ value: "homestack-agent", label: "Agent" }, { value: "tailscale", label: "Tailscale" }, { value: "jellyfin", label: "Jellyfin" }]} value={service} />}><ScrollArea className="log-view">{entries.map((entry) => <div key={entry.cursor}><time>{formatTime(entry.timestamp)}</time><code>{entry.message}</code></div>)}</ScrollArea>{cursor && <Button onClick={() => void load(cursor)} tone="default" variant="outline">加载后续</Button>}{error && <InlineNotice tone="danger">{error}</InlineNotice>}</AgentPage>;
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
  return <AgentPage title="Agent 更新"><div className="update-grid"><span>当前版本</span><strong>{status?.current_version || "-"}</strong><span>最新版本</span><strong>{status?.latest_version || "-"}</strong><span>状态</span><strong>{status?.state || "读取中"}</strong><span>签名</span><strong>{status?.signature || "-"}</strong></div>{status?.published_at && <p className="muted">发布于 {new Date(status.published_at).toLocaleString("zh-CN")}</p>}{status?.notes && <ScrollArea className="release-notes">{status.notes}</ScrollArea>}{status?.state === "downloading" && <Progress label="Agent 更新下载进度" value={percent} />}<div className="button-row"><Button disabled={busy} onClick={() => void check()} tone="default" variant="outline"><RefreshCw size={16} />检查更新</Button>{status?.state === "available" && <Button disabled={busy} loading={busy} onClick={() => void download()}><Download size={16} />下载并校验</Button>}{status?.state === "ready" && <Button onClick={() => void api<AgentUpdateStatus>("/api/updates/install", { method: "POST" }).then(setStatus).catch((reason) => setError(errorMessage(reason)))}><Settings size={16} />安装并重启</Button>}</div>{(error || status?.error) && <InlineNotice tone="danger">{error || status?.error}</InlineNotice>}</AgentPage>;
}

function AgentPage({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) { return <section className="agent-page"><PageHeader action={action} title={title} />{children}</section>; }
function Metric({ icon, label, value }: { icon: ReactNode; label: string; value: string }) { return <div className="metric"><span>{icon}</span><small>{label}</small><strong>{value}</strong></div>; }
function actionLabel(value: string) { return ({ start: "启动", stop: "停止", restart: "重启" } as Record<string, string>)[value] || value; }
function connectionTone(connection?: string): "success" | "warning" | "neutral" { return connection === "直连" ? "success" : connection === "自有中继" ? "warning" : "neutral"; }
