// frontend/src/app/services/theme.service.spec.ts
// Regression specs for FE-055 (theme flash on load): the resolved theme must match
// the pre-paint script in index.html (saved preference → OS preference → light),
// and an OS-derived default must not be persisted (so the system preference keeps
// following the system until the user explicitly toggles).
import { ThemeService } from './theme.service';

describe('ThemeService — FE-055 theme resolution', () => {
  const originalMatchMedia = window.matchMedia.bind(window);

  const stubPrefersDark = (dark: boolean): void => {
    window.matchMedia = ((query: string) =>
      ({
        matches: dark && query.includes('prefers-color-scheme: dark'),
        media: query,
        onchange: null,
        addListener: () => undefined,
        removeListener: () => undefined,
        addEventListener: () => undefined,
        removeEventListener: () => undefined,
        dispatchEvent: () => false,
      }) as unknown as MediaQueryList) as typeof window.matchMedia;
  };

  afterEach(() => {
    window.matchMedia = originalMatchMedia;
    localStorage.clear();
    document.documentElement.classList.add('light-theme');
    document.body.classList.add('light-theme');
  });

  it('FE-055: a saved dark theme is applied before anything else could paint light', () => {
    localStorage.setItem('theme', 'dark');
    stubPrefersDark(false);

    const service = new ThemeService();

    expect(service.currentTheme).toBe('dark');
    expect(document.documentElement.classList.contains('light-theme')).toBeFalse();
    expect(document.body.classList.contains('light-theme')).toBeFalse();
    expect(localStorage.getItem('theme')).toBe('dark');
  });

  it('FE-055: with no stored preference the OS preference wins and is NOT persisted', () => {
    localStorage.clear();
    stubPrefersDark(true);

    const service = new ThemeService();

    expect(service.currentTheme).toBe('dark');
    expect(document.documentElement.classList.contains('light-theme')).toBeFalse();
    expect(localStorage.getItem('theme'))
      .withContext('the OS preference must stay live until the user explicitly toggles')
      .toBeNull();
  });

  it('FE-055: light stays the default when neither storage nor the OS prefers dark', () => {
    localStorage.clear();
    stubPrefersDark(false);

    const service = new ThemeService();

    expect(service.currentTheme).toBe('light');
    expect(document.documentElement.classList.contains('light-theme')).toBeTrue();
  });

  it('FE-055: an explicit toggle persists the choice (afterwards it beats the OS preference)', () => {
    localStorage.clear();
    stubPrefersDark(false);

    const service = new ThemeService();
    service.toggleTheme(); // light -> dark

    expect(service.currentTheme).toBe('dark');
    expect(localStorage.getItem('theme')).toBe('dark');
  });
});
