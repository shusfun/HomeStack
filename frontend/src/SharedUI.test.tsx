// @vitest-environment jsdom

import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { Gauge, Settings } from "lucide-react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AppShell, ErrorBoundary } from "./components/ui";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

afterEach(() => vi.unstubAllGlobals());

function Broken(): ReactNode { throw new Error("渲染异常"); }

describe("共享 UI", () => {
  it("错误边界始终提供错误原因和重试入口", async () => {
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const container = document.createElement("div"); const root = createRoot(container);
    await act(async () => root.render(<ErrorBoundary><Broken /></ErrorBoundary>));
    expect(container.textContent).toContain("渲染异常");
    expect(container.textContent).toContain("重新加载");
    await act(async () => root.unmount());
  });

  it("AppShell 提供与桌面导航一致的移动菜单", async () => {
    vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
    const container = document.createElement("div"); document.body.appendChild(container); const root = createRoot(container);
    await act(async () => root.render(<MemoryRouter><AppShell nav={[{ to: "/", label: "概览", icon: Gauge, end: true }, { to: "/settings", label: "设置", icon: Settings }]} product="Agent"><div>内容</div></AppShell></MemoryRouter>));
    const trigger = container.querySelector<HTMLButtonElement>('button[aria-label="打开菜单"]');
    expect(trigger).not.toBeNull();
    await act(async () => trigger?.click());
    expect(document.body.textContent).toContain("导航");
    expect(document.body.textContent).toContain("设置");
    await act(async () => root.unmount()); container.remove();
  });
});
