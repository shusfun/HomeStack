import { describe, expect, it } from "vitest";
import { browserPublicHost, changeSetupProvider, normalizePublicAddress, oauthCallback, type SetupFormValue } from "./publicAddress";

describe("normalizePublicAddress", () => {
  it.each(["hs.waasabi.cloud", "https://hs.waasabi.cloud", "https://hs.waasabi.cloud/"])("规范化 %s", (input) => {
    expect(normalizePublicAddress(input)).toEqual({ host: "hs.waasabi.cloud", url: "https://hs.waasabi.cloud" });
  });

  it.each(["http://hs.waasabi.cloud", "https://hs.waasabi.cloud:443", "https://hs.waasabi.cloud/path", "127.0.0.1"])("拒绝 %s", (input) => {
    expect(normalizePublicAddress(input)).toBeNull();
  });
});

it("从 HTTPS 浏览器地址识别 VPS 域名", () => {
  expect(browserPublicHost({ protocol: "https:", hostname: "hs.waasabi.cloud", port: "" } as Location)).toBe("hs.waasabi.cloud");
  expect(browserPublicHost({ protocol: "http:", hostname: "127.0.0.1", port: "18443" } as Location)).toBe("");
});

it("切换 Provider 时同步回调并清空旧凭据", () => {
  const google: SetupFormValue = { public_host: "https://hs.waasabi.cloud/", provider: "google", client_id: "google-id", client_secret: "google-secret" };
  expect(oauthCallback(google.public_host, google.provider)).toBe("https://hs.waasabi.cloud/auth/callback/google");
  const github = changeSetupProvider(google, "github");
  expect(github).toMatchObject({ provider: "github", client_id: "", client_secret: "" });
  expect(oauthCallback(github.public_host, github.provider)).toBe("https://hs.waasabi.cloud/auth/callback/github");
});
