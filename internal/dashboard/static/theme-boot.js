
(function () {
  function boot() {
    var choice = null;
    try { choice = localStorage.getItem('aii.theme'); } catch (e) { choice = null; }
    var theme = choice;
    if (theme !== 'light' && theme !== 'dark') {
      theme = (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) ? 'light' : 'dark';
    }
    document.documentElement.setAttribute('data-theme', theme);
    return theme;
  }
  window.aiiThemeBoot = boot;
  boot();
})();
