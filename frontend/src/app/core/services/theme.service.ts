import { Injectable } from '@angular/core';
import { BehaviorSubject } from 'rxjs';

@Injectable({
  providedIn: 'root',
})
export class ThemeService {
  private activeTheme = new BehaviorSubject<'light' | 'dark'>('light');
  public theme$ = this.activeTheme.asObservable();

  constructor() {
    // FE-055: resolve the theme exactly like the pre-paint script in index.html
    // (saved preference → OS preference → light) and apply it *without* persisting,
    // so a not-yet-stated OS preference keeps following the system. Only an explicit
    // toggleTheme() writes the choice to localStorage.
    this.applyTheme(ThemeService.resolveTheme());
  }

  public get currentTheme(): 'light' | 'dark' {
    return this.activeTheme.value;
  }

  public toggleTheme(): void {
    const nextTheme = this.activeTheme.value === 'light' ? 'dark' : 'light';
    this.setTheme(nextTheme);
  }

  /** FE-055: saved preference, else `prefers-color-scheme`, else light. */
  private static resolveTheme(): 'light' | 'dark' {
    try {
      const savedTheme = localStorage.getItem('theme') as 'light' | 'dark' | null;
      if (savedTheme === 'light' || savedTheme === 'dark') {
        return savedTheme;
      }
    } catch {
      // Storage unavailable (private mode) — fall through to the OS preference.
    }
    if (
      typeof window !== 'undefined' &&
      typeof window.matchMedia === 'function' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches
    ) {
      return 'dark';
    }
    return 'light';
  }

  /** Persists the user's explicit choice (toggle) and applies it. */
  private setTheme(theme: 'light' | 'dark'): void {
    try {
      localStorage.setItem('theme', theme);
    } catch {
      // Storage unavailable — the theme still applies for this session.
    }
    this.applyTheme(theme);
  }

  private applyTheme(theme: 'light' | 'dark'): void {
    this.activeTheme.next(theme);

    if (theme === 'light') {
      document.documentElement.classList.add('light-theme');
      document.body.classList.add('light-theme');
    } else {
      document.documentElement.classList.remove('light-theme');
      document.body.classList.remove('light-theme');
    }
  }
}
