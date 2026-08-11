// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";

const detectSurface = vi.hoisted(() => vi.fn());
vi.mock("./api", async (importOriginal) => ({ ...(await importOriginal<typeof import("./api")>()), detectSurface }));

import App from "./App";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

describe("App 启动", () => {
  it("探测失败时显示真实错误并自动重试", async () => {
    vi.useFakeTimers();
    detectSurface.mockRejectedValueOnce(new Error("Control 无响应")).mockResolvedValueOnce("agent");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("Agent 尚未恢复")));
    const container = document.createElement("div"); const root = createRoot(container);
    await act(async () => { root.render(<MemoryRouter><App /></MemoryRouter>); });
    expect(container.textContent).toContain("Control 无响应");
    await act(async () => { await vi.advanceTimersByTimeAsync(2100); });
    expect(detectSurface).toHaveBeenCalledTimes(2);
    await act(async () => root.unmount());
  });
});
