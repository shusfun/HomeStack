// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { ManagedContentPreparation, type ManagedContentStatus } from "./DesktopApp";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const status: ManagedContentStatus = {
  state: "preparing", phase: "downloading", downloaded: 50, total: 100, speed_bps: 10,
  components: [
    { id: "filebrowser", label: "FileBrowser", version: "1.5.1-stable", phase: "ready", source_host: "ghproxy.net" },
    { id: "jellyfin", label: "Jellyfin", version: "10.11.11", phase: "downloading", downloaded: 50, total: 100, source_host: "repo.jellyfin.org" },
    { id: "jellyfin-ffmpeg", label: "Jellyfin FFmpeg", version: "7.1.4-3", phase: "pending" },
    { id: "node", label: "HomeStack Node", phase: "pending" },
  ],
};

describe("ManagedContentPreparation", () => {
  it("显示组件、来源和真实下载进度", async () => {
    const container = document.createElement("div"); const root = createRoot(container);
    const onCancel = vi.fn();
    await act(async () => root.render(<ManagedContentPreparation status={status} onCancel={onCancel} onResume={vi.fn()} />));
    expect(container.textContent).toContain("50%");
    expect(container.textContent).toContain("ghproxy.net");
    expect(container.textContent).toContain("repo.jellyfin.org");
    expect(container.textContent).toContain("HomeStack Node");
    expect(container.querySelector(".managed-progress")?.classList.contains("indeterminate")).toBe(false);
    expect(container.querySelector('button')?.textContent).toContain("取消");
    await act(async () => container.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(onCancel).toHaveBeenCalledOnce();
    await act(async () => root.unmount());
  });

  it("校验和配置阶段使用不确定进度", async () => {
    const container = document.createElement("div"); const root = createRoot(container);
    await act(async () => root.render(<ManagedContentPreparation status={{ ...status, phase: "verifying" }} onCancel={vi.fn()} onResume={vi.fn()} />));
    expect(container.querySelector(".managed-progress")?.classList.contains("indeterminate")).toBe(true);
    await act(async () => root.unmount());
  });

  it("失败后提供继续准备", async () => {
    const container = document.createElement("div"); const root = createRoot(container);
    const onResume = vi.fn();
    await act(async () => root.render(<ManagedContentPreparation status={{ ...status, state: "error", phase: "error", error: "下载失败" }} onCancel={vi.fn()} onResume={onResume} />));
    expect(container.textContent).toContain("继续准备");
    expect(container.textContent).toContain("下载失败");
    await act(async () => container.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })));
    expect(onResume).toHaveBeenCalledOnce();
    await act(async () => root.unmount());
  });
});
