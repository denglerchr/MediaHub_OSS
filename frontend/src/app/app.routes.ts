import { Routes } from '@angular/router';
import { authGuard } from './core/guards/auth.guard';
import { adminGuard } from './core/guards/admin.guard';
import { databaseAdminGuard, databaseGuard } from './core/guards/database.guard';

/**
 * Defines the application's routes using lazy-loaded standalone components.
 */
export const routes: Routes = [
  {
    path: 'login',
    loadComponent: () =>
      import('./features/auth/login-page/login-page.component').then((m) => m.LoginPageComponent),
  },
  {
    path: 'auth/callback',
    loadComponent: () =>
      import('./features/auth/auth-callback-page/auth-callback-page.component').then(
        (m) => m.AuthCallbackPageComponent,
      ),
  },
  {
    path: 'dashboard',
    loadComponent: () =>
      import('./features/dashboard/dashboard-page/dashboard-page.component').then(
        (m) => m.DashboardPageComponent,
      ),
    canActivate: [authGuard], // Protect this route and ALL its children with authGuard
    children: [
      // Child routes for the dashboard's <router-outlet>
      {
        path: 'db/:id/import',
        loadComponent: () =>
          import('./features/databases/import-page/import-page.component').then(
            (m) => m.ImportPageComponent,
          ),
        canActivate: [databaseAdminGuard], // Database admins can access import page
      },
      {
        path: 'db/:id',
        loadComponent: () =>
          import('./features/entries/entry-list/entry-list.component').then(
            (m) => m.EntryListComponent,
          ),
        canActivate: [databaseGuard], // Enforce database-level permissions
      },
      {
        path: 'settings/:id',
        loadComponent: () =>
          import('./features/databases/database-settings/database-settings.component').then(
            (m) => m.DatabaseSettingsComponent,
          ),
        canActivate: [databaseAdminGuard], // Database admins can access database settings
      },
      {
        path: 'admin/users',
        loadComponent: () =>
          import('./features/admin/admin-user-list/admin-user-list.component').then(
            (m) => m.AdminUserListComponent,
          ),
        canActivate: [adminGuard], // Enforce global admin permission
      },
      {
        path: 'admin/keys',
        loadComponent: () =>
          import('./features/admin/admin-global-keys/admin-global-keys.component').then(
            (m) => m.AdminGlobalKeysComponent,
          ),
        canActivate: [adminGuard],
      },
      {
        path: 'profile',
        loadComponent: () =>
          import('./features/profile/profile-page/profile-page.component').then(
            (m) => m.ProfilePageComponent,
          ),
      },
      {
        path: '',
        loadComponent: () =>
          import('./features/dashboard/overview-page/overview-page.component').then(
            (m) => m.OverviewPageComponent,
          ),
        pathMatch: 'full',
        data: { showSidebar: true },
      },
      {
        path: 'admin/audit',
        loadComponent: () =>
          import('./features/admin/admin-audit-log/admin-audit-log.component').then(
            (m) => m.AdminAuditLogComponent,
          ),
        canActivate: [adminGuard],
      },
    ],
  },
  {
    path: '',
    redirectTo: '/dashboard', // Default route redirects to dashboard
    pathMatch: 'full',
  },
  {
    path: '**', // Wildcard route for 404
    loadComponent: () =>
      import('./features/dashboard/page-not-found/page-not-found.component').then(
        (m) => m.PageNotFoundComponent,
      ),
  },
];
