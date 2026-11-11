// First: the live screen's token is read before App's module takes any token out of the address bar.
import { screen } from "./screen/take";
import { initI18n, startI18n } from "./i18n";
import "./styles.css";
import { applyLook } from "./ui/look";
import { logCaughtError } from "./lib/caught-error";

// Theme color and text size (docs/web/web.md "Theme color and text size") before anything draws.
applyLook();

// The language's catalog loads before the rest of the app: their modules call t() as they load (docs/web/web.md "Internationalization").
// The build waits only for the first screen's part of it, as its lazy imports wait for the rest; the dev server's do not.
// React and the PWA helper say no t(), so they load alongside it rather than after it.
// An overlay's history entry a reload left behind goes before the app reads the address (lib/back-close.ts, #5134).
const [, { StrictMode, createElement }, { createRoot }, { startPWA }] = await Promise.all([
  import.meta.env.DEV ? initI18n() : startI18n(),
  import("react"),
  import("react-dom/client"),
  import("./lib/pwa"),
  import("./lib/back-close").then((m) => m.settleOverlayEntry()),
]);

startPWA();

// The live screen (/screen/<machine>) is a page of its own: no sign-in flow, no sidebar, and noVNC loads only there.
const target = screen;
// a crash a boundary catches is logged with its component stack (lib/caught-error.ts)
const root = createRoot(document.getElementById("root")!, { onCaughtError: logCaughtError });
if (target) {
  const { ScreenView } = await import("./screen/ScreenView");
  root.render(createElement(StrictMode, null, createElement(ScreenView, { target })));
} else if (window.location.pathname === "/login/device") {
  const { NativePasswordLogin } = await import("./views/PasswordLogin");
  root.render(createElement(StrictMode, null, createElement(NativePasswordLogin)));
} else {
  // Error reports (docs/web/web.md "Error reports") start with the app: they go out only if the hub's hello has the feature.
  const [{ ExtensionThemeProvider }, { App }] = await Promise.all([import("./ui/ExtensionThemePicker"), import("./App"), import("./lib/error-reports-start")]);
  root.render(createElement(StrictMode, null, createElement(ExtensionThemeProvider, null, createElement(App))));
  // An open page picks up a new build (lib/new-version.ts): by itself when idle, else a toast that stays.
  const [{ startNewVersion }, { toast }, { t }] = await Promise.all([import("./lib/new-version"), import("./ui/atoms"), import("./i18n")]);
  startNewVersion((reloadNow) => toast(t("app.new_version"), "info", { label: t("app.new_version_reload"), run: reloadNow, sticky: true }));
}
