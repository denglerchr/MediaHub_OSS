import {
  Component,
  ChangeDetectorRef,
  DestroyRef,
  OnInit,
  Input,
  Output,
  EventEmitter,
  inject,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { RouterLink, RouterLinkActive } from '@angular/router';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Observable } from 'rxjs';
import { User, AppInfo } from '../../../core/models';
import { AuthService } from '../../../core/services/auth.service';
import { AppInfoService } from '../../../core/services/app-info.service';
import { ThemeService } from '../../../core/services/theme.service';

@Component({
  selector: 'app-sidebar',
  templateUrl: './sidebar.component.html',
  styleUrls: ['./sidebar.component.css'],
  standalone: true,
  imports: [CommonModule, RouterLink, RouterLinkActive],
})
export class SidebarComponent implements OnInit {
  @Input() isCollapsed: boolean = true;
  @Output() toggleSidebar = new EventEmitter<void>();

  public currentUser$: Observable<User | null>;
  // FE-029: single subscriptions instead of 5× `isLightTheme$ | async` and
  // 2× `appInfo$ | async` in the template. (The report's `*ngIf="… | async as light"`
  // suggestion cannot work — the stream emits a boolean, so a `false` would hide the
  // whole block; a plain property keeps one subscription and no re-subscribes.)
  public isLightTheme = true;
  public appInfo: AppInfo | null = null;

  private destroyRef = inject(DestroyRef);

  constructor(
    private authService: AuthService,
    private appInfoService: AppInfoService,
    private themeService: ThemeService,
    private cdr: ChangeDetectorRef,
  ) {
    this.currentUser$ = this.authService.currentUser$;
    this.themeService.theme$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((theme) => {
      this.isLightTheme = theme === 'light';
      this.cdr.markForCheck();
    });
    this.appInfoService.info$.pipe(takeUntilDestroyed(this.destroyRef)).subscribe((info) => {
      this.appInfo = info;
      this.cdr.markForCheck();
    });
  }

  toggleTheme(): void {
    this.themeService.toggleTheme();
  }

  ngOnInit(): void {
    this.appInfoService.loadInfo().pipe(takeUntilDestroyed(this.destroyRef)).subscribe();
  }

  onToggleSidebar(): void {
    this.toggleSidebar.emit();
  }

  logout(): void {
    this.authService.logout();
  }
}
