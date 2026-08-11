import type { APIErrorResponse, Surface } from "./types";

export async function detectSurface(): Promise<Surface> {
  if (isNativeWailsBridge()) return "desktop";
  const failures: string[] = [];
  const hosted = await probeSurface("/api/meta", failures);
  if (hosted === "control" || hosted === "agent") return hosted;
  const setup = await probeSurface("/api/setup/status", failures);
  if (setup === "setup") return "setup";
  throw new Error(`无法识别 HomeStack 服务：${failures.join("；")}`);
}

export function isNativeWailsBridge(): boolean {
  const browserWindow = globalThis as typeof globalThis & {
    wails?: { invoke?: unknown; invokeAsync?: unknown };
    _wails?: { environment?: { OS?: unknown } };
  };
  return typeof browserWindow.wails?.invoke === "function"
    || typeof browserWindow.wails?.invokeAsync === "function"
    || typeof browserWindow._wails?.environment?.OS === "string";
}

async function probeSurface(path: string, failures: string[]): Promise<Surface | null> {
  try {
    const response = await fetch(path, { credentials: "same-origin" });
    if (!response.ok) { failures.push(`${path} 返回 HTTP ${response.status}`); return null; }
    if (!response.headers.get("content-type")?.includes("application/json")) { failures.push(`${path} 返回了非 JSON 内容`); return null; }
    const surface = ((await response.json()) as { surface?: string }).surface;
    if (surface === "setup" || surface === "control" || surface === "agent" || surface === "desktop") return surface;
    failures.push(`${path} 缺少有效 surface`);
    return null;
  } catch (reason) {
    failures.push(`${path} 请求失败：${reason instanceof Error ? reason.message : String(reason)}`);
    return null;
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: "same-origin",
  });
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    const text = await response.text();
    if (text.trim()) {
      message = text.trim();
      try {
        const payload = JSON.parse(text) as APIErrorResponse;
        if (payload.error?.message) message = payload.error.message;
      } catch {
        // 非 JSON 错误响应直接保留服务端原文。
      }
    }
    throw new Error(message);
  }
  if (response.status === 204) return undefined as T;
  return (await response.json()) as T;
}

export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "-";
  if (value < 1024) return `${value} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let result = value;
  let unit = -1;
  while (result >= 1024 && unit < units.length - 1) {
    result /= 1024;
    unit += 1;
  }
  return `${result.toFixed(result >= 10 ? 1 : 2)} ${units[unit]}`;
}

export function formatTime(value?: string): string {
  if (!value) return "尚未上报";
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return "时间无效";
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(date);
}
