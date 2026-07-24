import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import "./i18n";
import App from "./App.tsx";
import { AuthGate } from "./components/auth-gate.tsx";
import { trackKeyboardInset } from "./lib/keyboard-inset";

// Follow the system color scheme; shadcn theming keys off the .dark class.
const darkQuery = window.matchMedia("(prefers-color-scheme: dark)");
const applyTheme = () => {
  document.documentElement.classList.toggle("dark", darkQuery.matches);
};
applyTheme();
darkQuery.addEventListener("change", applyTheme);

// iOS keeps the layout viewport full-height behind the soft keyboard; this
// publishes the covered height as --keyboard-inset for the root to subtract.
trackKeyboardInset();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <AuthGate>
      <App />
    </AuthGate>
  </StrictMode>,
);
