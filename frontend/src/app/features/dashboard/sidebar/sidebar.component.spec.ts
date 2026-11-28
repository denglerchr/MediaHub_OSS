import { ChangeDetectionStrategy, Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { provideRouter, RouterLink } from '@angular/router';
import { BehaviorSubject, of } from 'rxjs';
import { SidebarComponent } from './sidebar.component';
import { AuthService } from '../../../core/services/auth.service';
import { AppInfoService } from '../../../core/services/app-info.service';
import { ThemeService } from '../../../core/services/theme.service';
import { AppInfo, User } from '../../../core/models';

@Component({
  standalone: true,
  imports: [SidebarComponent],
  template: `<app-sidebar [isCollapsed]="false"></app-sidebar>`,
  changeDetection: ChangeDetectionStrategy.OnPush,
})
class OnPushSidebarHostComponent {}

describe('SidebarComponent', () => {
  let fixture: ComponentFixture<SidebarComponent>;
  let component: SidebarComponent;
  let currentUser$: BehaviorSubject<User | null>;
  let info$: BehaviorSubject<AppInfo | null>;
  let theme$: BehaviorSubject<'light' | 'dark'>;

  const testUser: User = {
    id: 'u1',
    username: 'alice',
    is_admin: false,
    account_type: 'local',
    permissions: [],
  };

  beforeEach(async () => {
    currentUser$ = new BehaviorSubject<User | null>(testUser);
    info$ = new BehaviorSubject<AppInfo | null>(null);
    theme$ = new BehaviorSubject<'light' | 'dark'>('light');

    const authSpy = jasmine.createSpyObj<AuthService>('AuthService', ['logout'], {
      currentUser$: currentUser$.asObservable(),
    });
    const appInfoSpy = jasmine.createSpyObj<AppInfoService>('AppInfoService', ['loadInfo'], {
      info$: info$.asObservable(),
    });
    appInfoSpy.loadInfo.and.returnValue(of(null));
    const themeSpy = jasmine.createSpyObj<ThemeService>('ThemeService', ['toggleTheme'], {
      theme$: theme$.asObservable(),
    });

    await TestBed.configureTestingModule({
      imports: [SidebarComponent, OnPushSidebarHostComponent],
      providers: [
        provideRouter([]),
        { provide: AuthService, useValue: authSpy },
        { provide: AppInfoService, useValue: appInfoSpy },
        { provide: ThemeService, useValue: themeSpy },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(SidebarComponent);
    component = fixture.componentInstance;
  });

  it('keeps the profile avatar visible and linked to /dashboard/profile when collapsed', () => {
    component.isCollapsed = true;
    fixture.detectChanges();

    const userInfoEl = fixture.nativeElement.querySelector('.user-info') as HTMLElement | null;
    expect(userInfoEl).toBeTruthy();
    expect(userInfoEl?.getAttribute('title')).toBe('View Profile & Settings (alice)');
    expect(userInfoEl?.querySelector('.user-avatar')?.textContent?.trim()).toBe('A');
    expect(userInfoEl?.querySelector('.user-details')).toBeNull();

    const routerLinkDebug = fixture.debugElement.query(By.css('.user-info'));
    const routerLinkInstance = routerLinkDebug.injector.get(RouterLink);
    expect(routerLinkInstance.urlTree?.toString()).toBe('/dashboard/profile');
  });

  it('shows user details and standard title when expanded', () => {
    component.isCollapsed = false;
    fixture.detectChanges();

    const userInfoEl = fixture.nativeElement.querySelector('.user-info') as HTMLElement | null;
    expect(userInfoEl).toBeTruthy();
    expect(userInfoEl?.getAttribute('title')).toBe('View Profile & Settings');
    expect(userInfoEl?.querySelector('.user-details')).toBeTruthy();
    expect(userInfoEl?.querySelector('.username')?.textContent?.trim()).toBe('alice');
  });

  it('C1: marks view for check and updates rendered DOM on async info$ and theme$ emissions', () => {
    const hostFixture = TestBed.createComponent(OnPushSidebarHostComponent);
    hostFixture.detectChanges();

    expect(hostFixture.nativeElement.querySelector('.app-version')).toBeNull();
    expect(
      hostFixture.nativeElement.querySelector('.theme-toggle-button span')?.textContent?.trim(),
    ).toBe('Dark Mode');

    info$.next({
      service_name: 'MediaHub',
      version: '2.4.0',
      uptime: '1h',
    });
    theme$.next('dark');
    hostFixture.detectChanges();

    const versionEl = hostFixture.nativeElement.querySelector('.app-version') as HTMLElement | null;
    expect(versionEl?.textContent).toContain('2.4.0');
    expect(
      hostFixture.nativeElement.querySelector('.theme-toggle-button span')?.textContent?.trim(),
    ).toBe('Light Mode');
  });
});
