import { Check, CircleAlert, LoaderCircle } from "lucide-react";
import type { ReactNode } from "react";

export function Page({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return <section className="page"><header className="page-header"><h1>{title}</h1>{action}</header>{children}</section>;
}

export function Feedback({ message, error }: { message?: string; error?: string }) {
  return <>{message && <div className="feedback success"><Check size={17} />{message}</div>}{error && <div className="feedback error"><CircleAlert size={17} />{error}</div>}</>;
}

export function CenteredLoader({ label, compact = false }: { label: string; compact?: boolean }) {
  return <div className={`centered-loader ${compact ? "compact" : ""}`}><LoaderCircle className="spin" size={22} /><span>{label}</span></div>;
}

export function EmptyState({ icon, label }: { icon: ReactNode; label: string }) {
  return <div className="empty-state">{icon}<span>{label}</span></div>;
}

export function StatusPill({ label, tone }: { label: string; tone: string }) {
  return <span className={`status-pill ${tone}`}>{label}</span>;
}

export function errorMessage(reason: unknown) {
  return reason instanceof Error ? reason.message : String(reason);
}

export function connectionTone(connection?: string) {
  return connection === "直连" ? "good" : connection === "自有中继" ? "warn" : "muted";
}
