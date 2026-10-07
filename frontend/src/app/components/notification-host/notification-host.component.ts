import {
  Component,
  OnDestroy,
  OnInit,
  ChangeDetectorRef,
  ChangeDetectionStrategy,
} from '@angular/core';
import { Notification, NotificationService } from '../../services/notification.service';
import { Subject } from 'rxjs';
import { takeUntil } from 'rxjs/operators';
import { trigger, transition, style, animate } from '@angular/animations';

@Component({
  selector: 'app-notification-host',
  templateUrl: './notification-host.component.html',
  styleUrls: ['./notification-host.component.css'],
  standalone: false,
  changeDetection: ChangeDetectionStrategy.OnPush, // NEW: Huge performance boost for a root-level component
  animations: [
    trigger('toastAnimation', [
      transition(':enter', [
        style({ transform: 'translateY(100%)', opacity: 0 }),
        animate('300ms ease-out', style({ transform: 'translateY(0)', opacity: 1 })),
      ]),
      transition(':leave', [
        animate('300ms ease-in', style({ transform: 'translateY(100%)', opacity: 0 })),
      ]),
    ]),
  ],
})
export class NotificationHostComponent implements OnInit, OnDestroy {
  public globalError: string | null = null;
  public toast: Notification | null = null;

  private destroy$ = new Subject<void>();
  private toastTimer?: ReturnType<typeof setTimeout>; // UPDATED: Strict typing instead of 'any'
  // FE-038: toasts queue up — a new toast must not silently replace an
  // unexpired one (and the old BehaviorSubject must not replay stale ones).
  private toastQueue: Notification[] = [];

  constructor(
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.notificationService.globalError$.pipe(takeUntil(this.destroy$)).subscribe((message) => {
      this.globalError = message;
      this.cdr.markForCheck(); // Inform Angular to update the view
    });

    this.notificationService.toastNotification$
      .pipe(takeUntil(this.destroy$))
      .subscribe((notification) => {
        this.toastQueue.push(notification);
        this.showNextToast();
      });
  }

  // FE-038: display one toast at a time, FIFO, 4 s each.
  private showNextToast(): void {
    if (this.toast || this.toastQueue.length === 0) {
      return;
    }

    this.toast = this.toastQueue.shift() ?? null;
    this.cdr.markForCheck(); // Inform Angular to update the view

    this.toastTimer = setTimeout(() => {
      this.toast = null;
      this.cdr.markForCheck(); // Inform Angular to update the view when hiding
      this.showNextToast();
    }, 4000);
  }

  clearGlobalError(): void {
    this.notificationService.clearGlobalError();
    this.globalError = null;
    this.cdr.markForCheck();
  }

  clearToast(): void {
    if (this.toastTimer) {
      clearTimeout(this.toastTimer);
      this.toastTimer = undefined;
    }
    this.toast = null;
    this.cdr.markForCheck();
    this.showNextToast();
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
    if (this.toastTimer) {
      clearTimeout(this.toastTimer);
    }
    this.toastQueue = [];
  }
}
