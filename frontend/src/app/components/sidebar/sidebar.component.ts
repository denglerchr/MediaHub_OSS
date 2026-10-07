import { Component, OnDestroy, OnInit, Input, Output, EventEmitter } from '@angular/core';
import { Observable, Subject } from 'rxjs';
import { takeUntil } from 'rxjs/operators';
import { User, AppInfo } from '../../models';
import { AuthService } from '../../services/auth.service';
import { AppInfoService } from '../../services/app-info.service';
import { ThemeService } from '../../services/theme.service';

@Component({
  selector: 'app-sidebar',
  templateUrl: './sidebar.component.html',
  styleUrls: ['./sidebar.component.css'],
  standalone: false,
})
export class SidebarComponent implements OnInit, OnDestroy {
  @Input() isCollapsed: boolean = true;
  @Output() toggleSidebar = new EventEmitter<void>();

  public currentUser$: Observable<User | null>;
  // FE-029: single subscriptions instead of 5× `isLightTheme$ | async` and
  // 2× `appInfo$ | async` in the template. (The report's `*ngIf="… | async as light"`
  // suggestion cannot work — the stream emits a boolean, so a `false` would hide the
  // whole block; a plain property keeps one subscription and no re-subscribes.)
  public isLightTheme = true;
  public appInfo: AppInfo | null = null;

  private destroy$ = new Subject<void>();

  constructor(
    private authService: AuthService,
    private appInfoService: AppInfoService,
    private themeService: ThemeService,
  ) {
    this.currentUser$ = this.authService.currentUser$;
    this.themeService.theme$.pipe(takeUntil(this.destroy$)).subscribe((theme) => {
      this.isLightTheme = theme === 'light';
    });
    this.appInfoService.info$.pipe(takeUntil(this.destroy$)).subscribe((info) => {
      this.appInfo = info;
    });
  }

  toggleTheme(): void {
    this.themeService.toggleTheme();
  }

  ngOnInit(): void {
    this.appInfoService.loadInfo().pipe(takeUntil(this.destroy$)).subscribe();
  }

  onToggleSidebar(): void {
    this.toggleSidebar.emit();
  }

  logout(): void {
    this.authService.logout();
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
