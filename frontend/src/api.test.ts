import { afterEach, describe, expect, it, vi } from "vitest";
import { api, detectSurface, formatBytes } from "./api";

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

  it("两种浏览器探测都失败时返回真实错误，不降级为 Desktop", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse({}, 503))
      .mockRejectedValueOnce(new Error("network down"));
    vi.stubGlobal("fetch", fetch);
    await expect(detectSurface()).rejects.toThrow("/api/meta 返回 HTTP 503");
    expect(fetch).toHaveBeenCalledTimes(2);
  });
});

describe("api", () => {
  it("保留服务端返回的纯文本错误", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("共享目录路径无效", {
      status: 400,
      headers: { "Content-Type": "text/plain" },
    })));

    await expect(api("/api/test")).rejects.toThrow("共享目录路径无效");
  });
});

function jsonResponse(value: unknown, status: number) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
