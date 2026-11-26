(function () {
  try {
    var saved = localStorage.getItem('theme');
    // N-D5: mirror ThemeService.resolveTheme() exactly — only 'light'/'dark'
    // are recognized; any other stored value falls back to the OS preference
    // (the old script forced light for unrecognized values and flashed after
    // bootstrap resolved dark).
    var osDark = !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
    var dark = saved === 'light' || saved === 'dark' ? saved === 'dark' : osDark;
    if (dark) {
      document.documentElement.classList.remove('light-theme');
    }
  } catch (e) {
    /* storage unavailable — keep the light default */
  }
})();
