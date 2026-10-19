// Applies the stored theme before first paint (loaded synchronously in <head>;
// an inline script would be blocked by the dashboard's CSP).
try {
  var t = localStorage.getItem("stackfork-theme");
  if (t === "light" || t === "dark") document.documentElement.dataset.theme = t;
} catch (e) { /* storage unavailable: the OS theme applies */ }
