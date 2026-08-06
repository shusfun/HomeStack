import { useEffect, useState } from "react";
import { detectSurface } from "./api";
import { AgentApp } from "./AgentApp";
import { ControlApp } from "./ControlApp";
import { DesktopApp } from "./DesktopApp";
import { CenteredLoader } from "./ui";
import type { Surface } from "./types";

export default function App() {
  const [surface, setSurface] = useState<Surface | null>(null);
  useEffect(() => { void detectSurface().then(setSurface); }, []);
  if (!surface) return <CenteredLoader label="正在连接" />;
  if (surface === "desktop") return <DesktopApp />;
  if (surface === "control") return <ControlApp />;
  return <AgentApp />;
}
