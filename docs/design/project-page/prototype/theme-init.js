// Same as the dashboard's theme-init.js: apply the stored theme before first
// paint. A ?theme=dark|light query wins (used for the screenshots).
try {
  var q = new URLSearchParams(location.search).get("theme");
  var t = q || localStorage.getItem("stackfork-theme");
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
} catch (e) { /* storage unavailable: the OS theme applies */ }
