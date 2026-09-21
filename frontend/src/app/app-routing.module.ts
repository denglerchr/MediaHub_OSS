import { NgModule } from '@angular/core';
import { RouterModule, Routes } from '@angular/router';
import { LoginPageComponent } from './pages/login-page/login-page.component';
import { AuthCallbackPageComponent } from './pages/auth-callback-page/auth-callback-page.component';
import { DashboardPageComponent } from './pages/dashboard-page/dashboard-page.component';
import { PageNotFoundComponent } from './pages/page-not-found/page-not-found.component';
import { AuthGuard } from './guards/auth.guard';
import { AdminGuard } from './guards/admin.guard';
import { DatabaseGuard } from './guards/database.guard';
import { DatabaseAdminGuard } from './guards/database-admin.guard';
import { EntryListComponent } from './components/entry-list/entry-list.component';
import { AdminAuditLogComponent } from './components/admin-audit-log/admin-audit-log.component';
import { DatabaseSettingsComponent } from './components/database-settings/database-settings.component';
import { AdminUserListComponent } from './components/admin-user-list/admin-user-list.component';
import { ImportPageComponent } from './pages/import-page/import-page.component';
import { OverviewPageComponent } from './pages/overview-page/overview-page.component';
import { ProfilePageComponent } from './pages/profile-page/profile-page.component';
import { AdminGlobalKeysComponent } from './components/admin-global-keys/admin-global-keys.component';

/**
 * Defines the application's routes.
 */
const routes: Routes = [
  {
    path: 'login',
    component: LoginPageComponent,
  },
  {
    path: 'auth/callback',
    component: AuthCallbackPageComponent,
  },
  {
    path: 'dashboard',
    component: DashboardPageComponent,
    canActivate: [AuthGuard], // Protect this route and ALL its children with AuthGuard
    children: [
      // Child routes for the dashboard's <router-outlet>
      {
        path: 'db/:id/import',
        component: ImportPageComponent,
        canActivate: [DatabaseAdminGuard], // Database admins can access import page
      },
      {
        path: 'db/:id',
        component: EntryListComponent,
        canActivate: [DatabaseGuard], // Enforce database-level CanView permissions
      },
      {
        path: 'settings/:id',
        component: DatabaseSettingsComponent,
        canActivate: [DatabaseAdminGuard], // Database admins can access database settings
      },
      {
        path: 'admin/users',
        component: AdminUserListComponent,
        canActivate: [AdminGuard], // Enforce global admin permission
      },
      {
        path: 'admin/keys',
        component: AdminGlobalKeysComponent,
        canActivate: [AdminGuard],
      },
      {
        path: 'profile',
        component: ProfilePageComponent,
      },
      {
        path: '',
        component: OverviewPageComponent,
        pathMatch: 'full',
      },
      {
        path: 'admin/audit',
        component: AdminAuditLogComponent,
        canActivate: [AdminGuard],
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
    component: PageNotFoundComponent,
  },
];

@NgModule({
  imports: [RouterModule.forRoot(routes)],
  exports: [RouterModule],
})
export class AppRoutingModule {}