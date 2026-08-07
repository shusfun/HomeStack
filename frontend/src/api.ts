import type { APIErrorResponse, Surface } from "./types";

export async function detectSurface(): Promise<Surface> {
  const hosted = await probeSurface("/api/meta");
  if (hosted === "control" || hosted === "agent") return hosted;
  const setup = await probeSurface("/api/setup/status");
  if (setup === "setup") return "setup";
  return "desktop";
}

async function probeSurface(path: string): Promise<Surface | null> {
  try {
    const response = await fetch(path, { credentials: "same-origin" });
    if (!response.ok || !response.headers.get("content-type")?.includes("application/json")) return null;
    const surface = ((await response.json()) as { surface?: string }).surface;
    return surface === "setup" || surface === "control" || surface === "agent" || surface === "desktop" ? surface : null;
  } catch {
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
    try {
      const payload = (await response.json()) as APIErrorResponse;
      if (payload.error?.message) message = payload.error.message;
    } catch {
      const text = await response.text();
      if (text.trim()) message = text.trim();
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
