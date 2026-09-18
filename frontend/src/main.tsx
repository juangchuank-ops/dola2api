import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import "@/shared/i18n";
import "@/index.css";
import { App } from "@/app/app";

const container = document.getElementById("root");
if (!container) throw new Error("root container is missing");

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
