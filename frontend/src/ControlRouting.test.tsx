// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ControlApp } from "./ControlApp";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

afterEach(() => vi.unstubAllGlobals());

describe("ControlApp 路由", () => {
  it("首次从根地址跳转登录页后立即渲染内容", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ providers: [{ id: "github", label: "GitHub" }], me: null }), { status: 200, headers: { "Content-Type": "application/json" } })));
    const container = document.createElement("div");
    const root = createRoot(container);
    await act(async () => { root.render(<MemoryRouter initialEntries={["/"]}><ControlApp /></MemoryRouter>); });
    expect(container.textContent).toContain("登录 HomeStack");
    expect(container.querySelector('a[href="/auth/login/github?return=/"]')).not.toBeNull();
    await act(async () => root.unmount());
  });
});
