// Resolve the stored theme (system | light | dark) before React mounts, so the
// first paint already uses the right palette. Kept in sync with src/lib/theme.tsx.
// Also paints the last accent this browser saw (src/lib/accent.ts caches it as
// {attr, vars}); the account's own value replaces it once /api/auth/me answers.
(function () {
  var root = document.documentElement;
  var pref = "system";
  try {
    pref = localStorage.getItem("forge.theme") || "system";
  } catch (e) {
    /* storage blocked: fall back to the system preference */
  }
  var dark =
    pref === "dark" ||
    (pref !== "light" && window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches);
  root.setAttribute("data-theme", dark ? "dark" : "light");

  try {
    var accent = JSON.parse(localStorage.getItem("forge.accent") || "null");
    if (accent && typeof accent.attr === "string" && /^[a-z]+$/.test(accent.attr)) {
      root.setAttribute("data-accent", accent.attr);
      var vars = accent.vars || {};
      for (var k in vars) {
        if (/^--accent-c-[a-z]+-[ld]$/.test(k) && /^#[0-9a-f]{6}$/.test(vars[k])) root.style.setProperty(k, vars[k]);
      }
    }
  } catch (e) {
    /* nothing cached, or storage blocked: the default accent */
  }
})();
