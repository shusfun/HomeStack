// @vitest-environment jsdom

import { act } from "react";
import { createRoot } from "react-dom/client";
import { MemoryRouter, useLocation } from "react-router-dom";
import { describe, expect, it } from "vitest";
import { DesktopPageHeader } from "./DesktopPageHeader";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function CurrentPath() {
  return <output data-testid="current-path">{useLocation().pathname}</output>;
}

describe("DesktopPageHeader", () => {
  it.each(["/updates", "/settings"])("从 %s 固定返回设备页", async (initialPath) => {
    const container = document.createElement("div");
    const root = createRoot(container);

    await act(async () => {
      root.render(
        <MemoryRouter initialEntries={[initialPath]}>
          <DesktopPageHeader title="子页面" />
          <CurrentPath />
        </MemoryRouter>,
      );
    });

    const backLink = container.querySelector<HTMLAnchorElement>('a[aria-label="返回设备"]');
    expect(backLink?.getAttribute("href")).toBe("/");

    await act(async () => {
      backLink?.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    });

    expect(container.querySelector('[data-testid="current-path"]')?.textContent).toBe("/");

    await act(async () => root.unmount());
  });
});
