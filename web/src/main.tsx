import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { isMock } from "./api";
import "./styles.css";

// Service worker = Web Push only (approvals / task results on any device).
if ("serviceWorker" in navigator && !isMock()) {
  navigator.serviceWorker.register("./sw.js", { scope: "./" }).catch((e) => console.warn("[life-agent] SW", e));
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
