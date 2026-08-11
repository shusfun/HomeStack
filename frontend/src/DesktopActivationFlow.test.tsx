// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const call = vi.hoisted(() => vi.fn());
vi.mock("@wailsio/runtime", () => ({
  Call: { ByName: call },
  Events: { On: vi.fn(() => () => undefined) },
  System: { IsMac: vi.fn(() => true) },
  Window: { Minimise: vi.fn(), ToggleMaximise: vi.fn(), Close: vi.fn() },
}));

import { DesktopApp } from "./DesktopApp";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("Desktop 激活后流程", () => {
  it("组件准备失败时仍显示已登录主界面", async () => {
    const failed = { state: "error", phase: "error", error: "组件下载失败", components: [] };
    call.mockImplementation((name: string) => {
      if (name.endsWith(".Session")) return Promise.resolve({ logged_in: true, control_url: "https://hs.example.com" });
      if (name.endsWith(".ManagedContentStatus") || name.endsWith(".EnsureManagedContentPreparation")) return Promise.resolve(failed);
      if (name.endsWith(".LocalStatus")) return Promise.resolve({ online: true, tailnet_ip: "100.64.0.1", connection: "直连" });
      if (name.endsWith(".Devices")) return Promise.resolve([]);
      if (name.endsWith(".CheckForUpdates")) return Promise.resolve({ state: "up-to-date" });
      return Promise.resolve(undefined);
    });
    const container = document.createElement("div"); const root = createRoot(container);
    await act(async () => root.render(<MemoryRouter><DesktopApp /></MemoryRouter>));
    expect(container.textContent).toContain("设备");
    expect(container.textContent).toContain("本机能力准备失败");
    expect(container.textContent).not.toContain("一次性激活码");
    expect(call).toHaveBeenCalledWith(expect.stringContaining("EnsureManagedContentPreparation"));
    await act(async () => root.unmount());
  });
});
