// frontend/src/app/pages/page-not-found/page-not-found.component.ts

import { Component } from '@angular/core';

@Component({
  selector: 'app-page-not-found',
  template: `
    <div style="text-align: center; padding-top: 5rem; font-family: Arial, sans-serif;">
      <h1>404 - Not Found</h1>
      <p>The page you are looking for does not exist.</p>
      <!-- FE-054: the label promises the dashboard — link there (the root '/' route
           redirected elsewhere and mismatched the label). Unauthenticated users are
           sent to /login by the auth guard, as for every dashboard link. -->
      <a routerLink="/dashboard">Go to Dashboard</a>
    </div>
  `,
  standalone: false,
})
export class PageNotFoundComponent {}
