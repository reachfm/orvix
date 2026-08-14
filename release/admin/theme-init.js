// Pre-paint theme init: must run synchronously, before any CSS paints,
// to avoid a flash of the wrong theme. Light is the mandatory default —
// only an explicit prior "dark" choice in localStorage under
// orvix-admin-theme switches to dark. Kept in sync with
// src/shared/theme/theme.ts's getStoredTheme/applyTheme. Served as an
// external file (not inlined in index.html) because the admin page's
// CSP is script-src 'self' with no inline-script allowance.
(function () {
  try {
    if (window.localStorage.getItem("orvix-admin-theme") === "dark") {
      document.documentElement.classList.add("dark");
    }
  } catch (e) {}
})();
