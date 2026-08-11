// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { ControlUpdates } from "./ControlApp";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("ControlUpdates", () => {
  it("显示 VPS 版本并执行检查更新", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(jsonResponse({ state: "idle", current_version: "1.2.3", signature: "等待校验" }))
      .mockResolvedValueOnce(jsonResponse({ state: "available", current_version: "1.2.3", latest_version: "1.2.4", signature: "签名已声明，等待下载校验" }));
    vi.stubGlobal("fetch", fetch);
    const container = document.createElement("div"); const root = createRoot(container);
    await act(async () => { root.render(<ControlUpdates />); });
    expect(container.textContent).toContain("1.2.3");
    const check = Array.from(container.querySelectorAll("button")).find((button) => button.textContent?.includes("检查更新"));
    await act(async () => { check?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    expect(container.textContent).toContain("1.2.4");
    expect(container.textContent).toContain("下载并校验");
    expect(fetch).toHaveBeenNthCalledWith(2, "/api/system/updates/check", expect.objectContaining({ method: "POST", credentials: "same-origin" }));
    await act(async () => root.unmount());
    vi.unstubAllGlobals();
  });
});

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { "Content-Type": "application/json" } });
}
