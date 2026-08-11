import type { ReactNode } from "react";
import { Badge, EmptyState as SharedEmptyState, InlineNotice, Loading, PageHeader } from "./components/ui";

export function Page({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return <section className="page"><PageHeader action={action} title={title} />{children}</section>;
}

export function Feedback({ message, error }: { message?: string; error?: string }) {
  return <>{message && <InlineNotice tone="success">{message}</InlineNotice>}{error && <InlineNotice tone="danger">{error}</InlineNotice>}</>;
}

export function CenteredLoader({ label, compact = false }: { label: string; compact?: boolean }) {
  return <Loading compact={compact} label={label} />;
}

export function EmptyState({ icon, label }: { icon: ReactNode; label: string }) {
  return <SharedEmptyState icon={icon} title={label} />;
}

export function StatusPill({ label, tone }: { label: string; tone: string }) {
  const mapped = tone === "good" ? "success" : tone === "warn" ? "warning" : "neutral";
  return <Badge tone={mapped}>{label}</Badge>;
}

export function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}

export function connectionTone(connection?: string) {
  return connection === "直连" ? "good" : connection === "自有中继" ? "warn" : "muted";
}
