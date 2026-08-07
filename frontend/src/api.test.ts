import { afterEach, describe, expect, it, vi } from "vitest";
import { detectSurface, formatBytes } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("formatBytes", () => {
  it("按可读单位格式化文件大小", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.50 KB");
    expect(formatBytes(10 * 1024 * 1024)).toBe("10.0 MB");
  });
});

describe("detectSurface", () => {
  it("优先通过成功响应识别正式 Control", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(jsonResponse({ surface: "control" }, 200));
    vi.stubGlobal("fetch", fetch);
    await expect(detectSurface()).resolves.toBe("control");
    expect(fetch).toHaveBeenCalledOnce();
    expect(fetch).toHaveBeenCalledWith("/api/meta", { credentials: "same-origin" });
  });

  it("只接受带正确 surface 的成功 Setup 响应", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ error: { code: "not_found" } }, 404))
      .mockResolvedValueOnce(jsonResponse({ surface: "setup", phase: "token" }, 200));
    vi.stubGlobal("fetch", fetch);
    await expect(detectSurface()).resolves.toBe("setup");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("通过公开元数据识别 Agent", async () => {
    const fetch = vi.fn().mockResolvedValueOnce(jsonResponse({ surface: "agent" }, 200));
    vi.stubGlobal("fetch", fetch);
    await expect(detectSurface()).resolves.toBe("agent");
    expect(fetch).toHaveBeenCalledOnce();
  });
});

function jsonResponse(value: unknown, status: number) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
