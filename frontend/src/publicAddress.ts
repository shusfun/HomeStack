export type OAuthProviderID = "google" | "github";

export interface SetupFormValue {
  public_host: string;
  provider: OAuthProviderID;
  client_id: string;
  client_secret: string;
}

export function normalizePublicAddress(raw: string): { host: string; url: string } | null {
  const value = raw.trim();
  if (!value) return null;
  try {
    const candidate = value.includes("://") ? value : `https://${value}`;
    const authority = candidate.slice(candidate.indexOf("://") + 3).split(/[/?#]/, 1)[0];
    if (authority.includes(":")) return null;
    const parsed = new URL(candidate);
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.port || (parsed.pathname !== "/" && parsed.pathname !== "") || parsed.search || parsed.hash) return null;
    const host = parsed.hostname.toLowerCase();
    if (!validHostname(host)) return null;
    return { host, url: `https://${host}` };
  } catch {
    return null;
  }
}

export function browserPublicHost(location: Pick<Location, "protocol" | "hostname" | "port">): string {
  if (location.protocol !== "https:" || location.port) return "";
  return normalizePublicAddress(location.hostname)?.host || "";
}

export function oauthCallback(raw: string, provider: OAuthProviderID): string {
  const address = normalizePublicAddress(raw);
  return address ? `${address.url}/auth/callback/${provider}` : "";
}

export function changeSetupProvider(value: SetupFormValue, provider: OAuthProviderID): SetupFormValue {
  if (value.provider === provider) return value;
  return { ...value, provider, client_id: "", client_secret: "" };
}

function validHostname(host: string): boolean {
  if (host.length > 253 || /^\d+(?:\.\d+){3}$/.test(host)) return false;
  const labels = host.split(".");
  return labels.length >= 2 && labels.every((label) => label.length > 0 && label.length <= 63 && /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label));
}
