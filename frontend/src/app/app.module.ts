// frontend/src/app/app.module.ts

import { NgModule } from '@angular/core';
import { BrowserModule } from '@angular/platform-browser';
import { HttpClientModule, HTTP_INTERCEPTORS } from '@angular/common/http';
import { FormsModule, ReactiveFormsModule } from '@angular/forms';
import { BrowserAnimationsModule } from '@angular/platform-browser/animations';

import { AppRoutingModule } from './app-routing.module';
import { AppComponent } from './app.component';

// Components
import { LoginPageComponent } from './pages/login-page/login-page.component';
import { AuthCallbackPageComponent } from './pages/auth-callback-page/auth-callback-page.component';
import { DashboardPageComponent } from './pages/dashboard-page/dashboard-page.component';
import { PageNotFoundComponent } from './pages/page-not-found/page-not-found.component';
import { SidebarComponent } from './components/sidebar/sidebar.component';
import { EntryListComponent } from './components/entry-list/entry-list.component';
import { DatabaseSettingsComponent } from './components/database-settings/database-settings.component';
import { NotificationHostComponent } from './components/notification-host/notification-host.component';
import { ModalComponent } from './components/modal/modal.component';
import { CreateDatabaseModalComponent } from './components/create-database-modal/create-database-modal.component';
import { UploadEntryModalComponent } from './components/upload-entry-modal/upload-entry-modal.component';
import { EntryDetailModalComponent } from './components/entry-detail-modal/entry-detail-modal.component';
import { EditEntryModalComponent } from './components/edit-entry-modal/edit-entry-modal.component';
import { ConfirmationModalComponent } from './components/confirmation-modal/confirmation-modal.component';
import { AdminUserListComponent } from './components/admin-user-list/admin-user-list.component';
import { EntryGridComponent } from './components/entry-grid/entry-grid.component';
import { EntryListViewComponent } from './components/entry-list-view/entry-list-view.component';
import { EntryFilterComponent } from './components/entry-filter/entry-filter.component';
import { AdminAuditLogComponent } from './components/admin-audit-log/admin-audit-log.component';
import { DatetimeDefaultDirective } from './directives/datetime-default.directive';
import { FullscreenSettingsModalComponent } from './components/fullscreen-settings-modal/fullscreen-settings-modal.component';
import { FullscreenPlayerComponent } from './components/fullscreen-player/fullscreen-player.component';
import { ImportPageComponent } from './pages/import-page/import-page.component';
import { OverviewPageComponent } from './pages/overview-page/overview-page.component';
import { ProfilePageComponent } from './pages/profile-page/profile-page.component';
import { AdminGlobalKeysComponent } from './components/admin-global-keys/admin-global-keys.component';
import { ApiKeyModalComponent } from './components/api-key-modal/api-key-modal.component';

// Pipes & Directives
import { FormatBytesPipe } from './pipes/format-bytes.pipe';
import { SecureImageDirective } from './directives/secure-image.directive';
import { FileDragDropDirective } from './directives/file-drag-drop.directive';

// Interceptor
import { JwtInterceptor } from './interceptors/jwt.interceptor';

@NgModule({
  declarations: [
    AppComponent,
    LoginPageComponent,
    AuthCallbackPageComponent,
    DashboardPageComponent,
    SidebarComponent,
    EntryListComponent,
    DatabaseSettingsComponent,
    PageNotFoundComponent,
    NotificationHostComponent,
    ModalComponent,
    CreateDatabaseModalComponent,
    UploadEntryModalComponent,
    EntryDetailModalComponent,
    EditEntryModalComponent,
    ConfirmationModalComponent,
    AdminUserListComponent,
    EntryFilterComponent,
    AdminAuditLogComponent,
    FullscreenSettingsModalComponent,
    FullscreenPlayerComponent,
    ImportPageComponent,
    OverviewPageComponent,
    ProfilePageComponent,
    AdminGlobalKeysComponent,
    ApiKeyModalComponent,
  ],
  imports: [
    BrowserModule,
    AppRoutingModule,
    HttpClientModule,
    FormsModule,
    ReactiveFormsModule,
    BrowserAnimationsModule,
    EntryGridComponent,
    EntryListViewComponent,
    SecureImageDirective,
    FormatBytesPipe,
    FileDragDropDirective,
    DatetimeDefaultDirective,
  ],
  providers: [
    { provide: HTTP_INTERCEPTORS, useClass: JwtInterceptor, multi: true }
  ],
  bootstrap: [AppComponent],
})
export class AppModule {}