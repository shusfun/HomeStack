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
  it("不会把正式 Control 的 Setup 锁定响应识别成 Setup", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ error: { code: "setup_locked" } }, 423))
      .mockResolvedValueOnce(jsonResponse({ surface: "control" }, 200));
    vi.stubGlobal("fetch", fetch);
    await expect(detectSurface()).resolves.toBe("control");
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it("只接受带正确 surface 的成功 Setup 响应", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(jsonResponse({ surface: "setup", phase: "token" }, 200)));
    await expect(detectSurface()).resolves.toBe("setup");
  });
});

function jsonResponse(value: unknown, status: number) {
  return new Response(JSON.stringify(value), { status, headers: { "Content-Type": "application/json" } });
}
