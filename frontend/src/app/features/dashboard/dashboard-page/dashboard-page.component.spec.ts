import { NO_ERRORS_SCHEMA } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { ActivatedRoute, Event, NavigationEnd, Router } from '@angular/router';
import { Subject } from 'rxjs';
import { DashboardPageComponent } from './dashboard-page.component';
import { SidebarComponent } from '../sidebar/sidebar.component';
import { CreateDatabaseModalComponent } from '../../databases/create-database-modal/create-database-modal.component';
import { UploadEntryModalComponent } from '../../entries/upload-entry-modal/upload-entry-modal.component';
import { EntryDetailModalComponent } from '../../entries/entry-detail-modal/entry-detail-modal.component';
import { EditEntryModalComponent } from '../../entries/edit-entry-modal/edit-entry-modal.component';
import { ApiKeyModalComponent } from '../../admin/api-key-modal/api-key-modal.component';
import { FullscreenSettingsModalComponent } from '../../entries/fullscreen-settings-modal/fullscreen-settings-modal.component';

interface MockChildRoute {
  snapshot: { data: Record<string, unknown> };
  firstChild?: MockChildRoute | null;
}

describe('DashboardPageComponent', () => {
  let fixture: ComponentFixture<DashboardPageComponent>;
  let component: DashboardPageComponent;
  let events$: Subject<Event>;
  let mockActivatedRoute: { firstChild: MockChildRoute | null };

  beforeEach(async () => {
    events$ = new Subject<Event>();
    mockActivatedRoute = {
      firstChild: {
        snapshot: { data: { showSidebar: true } },
      },
    };

    const routerSpy = jasmine.createSpyObj<Router>('Router', ['navigate'], {
      events: events$.asObservable(),
    });

    await TestBed.configureTestingModule({
      imports: [DashboardPageComponent],
      providers: [
        { provide: Router, useValue: routerSpy },
        { provide: ActivatedRoute, useValue: mockActivatedRoute },
      ],
    })
      .overrideComponent(DashboardPageComponent, {
        remove: {
          imports: [
            SidebarComponent,
            CreateDatabaseModalComponent,
            UploadEntryModalComponent,
            EntryDetailModalComponent,
            EditEntryModalComponent,
            ApiKeyModalComponent,
            FullscreenSettingsModalComponent,
          ],
        },
        add: { schemas: [NO_ERRORS_SCHEMA] },
      })
      .compileComponents();

    fixture = TestBed.createComponent(DashboardPageComponent);
    component = fixture.componentInstance;
  });

  it('sets showSidebar to true when active child route has data.showSidebar === true', () => {
    fixture.detectChanges();
    expect(component.showSidebar).toBeTrue();
  });

  it('sets showSidebar to false when active child route does not have data.showSidebar === true', () => {
    mockActivatedRoute.firstChild = {
      snapshot: { data: {} },
    };

    fixture.detectChanges();
    expect(component.showSidebar).toBeFalse();
  });

  it('sets showSidebar to false when there is no active child route', () => {
    mockActivatedRoute.firstChild = null;

    fixture.detectChanges();
    expect(component.showSidebar).toBeFalse();
  });

  it('updates showSidebar on NavigationEnd based on the deepest active child route data', () => {
    fixture.detectChanges();
    expect(component.showSidebar).toBeTrue();

    mockActivatedRoute.firstChild = {
      snapshot: { data: { showSidebar: false } },
    };
    events$.next(new NavigationEnd(1, '/dashboard/db/1', '/dashboard/db/1'));
    fixture.detectChanges();
    expect(component.showSidebar).toBeFalse();

    mockActivatedRoute.firstChild = {
      snapshot: { data: {} },
      firstChild: {
        snapshot: { data: { showSidebar: true } },
      },
    };
    events$.next(new NavigationEnd(2, '/dashboard', '/dashboard'));
    fixture.detectChanges();
    expect(component.showSidebar).toBeTrue();
  });
});
