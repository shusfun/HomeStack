import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
import { ErrorBoundary } from "./components/ui";
import "./styles.css";
import "./setup.css";
import "./components/ui/ui.css";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary><BrowserRouter><App /></BrowserRouter></ErrorBoundary>
  </StrictMode>,
);
