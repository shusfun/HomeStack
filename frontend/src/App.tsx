import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { detectSurface } from "./api";
import { AuthLayout, Button, InlineNotice, Loading } from "./components/ui";
import type { Surface } from "./types";

const AgentApp = lazy(() => import("./AgentApp").then((module) => ({ default: module.AgentApp })));
const ControlApp = lazy(() => import("./ControlApp").then((module) => ({ default: module.ControlApp })));
const DesktopApp = lazy(() => import("./DesktopApp").then((module) => ({ default: module.DesktopApp })));
const SetupApp = lazy(() => import("./SetupApp").then((module) => ({ default: module.SetupApp })));

export default function App() {
  const [surface, setSurface] = useState<Surface | null>(null);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const retryTimer = useRef<number | undefined>(undefined);
  const connect = useCallback(async () => {
    window.clearTimeout(retryTimer.current);
    setError("");
    try { setSurface(await detectSurface()); }
    catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      setAttempt((value) => value + 1);
    }
  }, []);
  useEffect(() => { void connect(); return () => window.clearTimeout(retryTimer.current); }, [connect]);
  useEffect(() => {
    if (!error) return;
    const delay = Math.min(30_000, 1000 * (2 ** Math.min(attempt, 5)));
    retryTimer.current = window.setTimeout(() => void connect(), delay);
    return () => window.clearTimeout(retryTimer.current);
  }, [attempt, connect, error]);
  if (!surface && error) return <AuthLayout title="连接 HomeStack 失败" description="服务暂时不可用，页面会自动重试。"><InlineNotice tone="danger">{error}</InlineNotice><Button onClick={() => void connect()}>立即重试</Button></AuthLayout>;
  if (!surface) return <Loading label="正在连接 HomeStack" />;
  const content = surface === "setup" ? <SetupApp /> : surface === "desktop" ? <DesktopApp /> : surface === "control" ? <ControlApp /> : <AgentApp />;
  return <Suspense fallback={<Loading label="正在加载界面" />}>{content}</Suspense>;
}
