// Resolve the stored theme (system | light | dark) before React mounts, so the
// first paint already uses the right palette. Kept in sync with src/lib/theme.tsx.
(function () {
  var pref = "system";
  try {
    pref = localStorage.getItem("forge.theme") || "system";
  } catch (e) {
    /* storage blocked: fall back to the system preference */
  }
  var dark =
    pref === "dark" ||
    (pref !== "light" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.setAttribute("data-theme", dark ? "dark" : "light");
})();
