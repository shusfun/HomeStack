import { describe, expect, it } from "vitest";
import { resolveFileItemPath } from "./AgentApp";

describe("Agent 文件路径", () => {
  it("根目录使用服务端返回的目录 ID，不使用显示名称拼接", () => {
    expect(resolveFileItemPath("/", { name: "下载", path: "/downloads" })).toBe("/downloads");
  });

  it("普通子目录继续按当前虚拟路径拼接", () => {
    expect(resolveFileItemPath("/downloads", { name: "照片" })).toBe("/downloads/照片");
  });
});
