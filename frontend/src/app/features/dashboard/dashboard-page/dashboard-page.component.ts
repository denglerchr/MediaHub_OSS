import { Component, DestroyRef, OnInit, ChangeDetectorRef, inject } from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, Router, NavigationEnd, RouterOutlet } from '@angular/router';
import { filter } from 'rxjs/operators';
import { SidebarComponent } from '../sidebar/sidebar.component';
import { CreateDatabaseModalComponent } from '../../databases/create-database-modal/create-database-modal.component';
import { UploadEntryModalComponent } from '../../entries/upload-entry-modal/upload-entry-modal.component';
import { EntryDetailModalComponent } from '../../entries/entry-detail-modal/entry-detail-modal.component';
import { EditEntryModalComponent } from '../../entries/edit-entry-modal/edit-entry-modal.component';
import { ApiKeyModalComponent } from '../../admin/api-key-modal/api-key-modal.component';
import { FullscreenSettingsModalComponent } from '../../entries/fullscreen-settings-modal/fullscreen-settings-modal.component';

@Component({
  selector: 'app-dashboard-page',
  templateUrl: './dashboard-page.component.html',
  styleUrls: ['./dashboard-page.component.css'],
  standalone: true,
  imports: [
    CommonModule,
    RouterOutlet,
    SidebarComponent,
    CreateDatabaseModalComponent,
    UploadEntryModalComponent,
    EntryDetailModalComponent,
    EditEntryModalComponent,
    ApiKeyModalComponent,
    FullscreenSettingsModalComponent,
  ],
})
export class DashboardPageComponent implements OnInit {
  public isSidebarCollapsed: boolean = true;
  public showSidebar: boolean = true;

  private destroyRef = inject(DestroyRef);

  constructor(
    private router: Router,
    private route: ActivatedRoute,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.updateSidebarVisibility();

    this.router.events
      .pipe(
        filter((event): event is NavigationEnd => event instanceof NavigationEnd),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe(() => {
        this.updateSidebarVisibility();
        this.cdr.markForCheck();
      });
  }

  private updateSidebarVisibility(): void {
    let child: ActivatedRoute | null = this.route.firstChild;
    while (child?.firstChild) {
      child = child.firstChild;
    }
    this.showSidebar = child?.snapshot?.data?.['showSidebar'] === true;
  }

  toggleSidebar(): void {
    this.isSidebarCollapsed = !this.isSidebarCollapsed;
  }
}
