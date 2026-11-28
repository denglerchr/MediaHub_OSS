import {
  Component,
  DestroyRef,
  OnDestroy,
  OnInit,
  ChangeDetectorRef,
  ChangeDetectionStrategy,
  inject,
} from '@angular/core';
import { CommonModule } from '@angular/common';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Notification, NotificationService } from '../../../core/services/notification.service';

@Component({
  selector: 'app-notification-host',
  templateUrl: './notification-host.component.html',
  styleUrls: ['./notification-host.component.css'],
  standalone: true,
  imports: [CommonModule],
  changeDetection: ChangeDetectionStrategy.OnPush, // NEW: Huge performance boost for a root-level component
})
export class NotificationHostComponent implements OnInit, OnDestroy {
  private static readonly MAX_TOAST_QUEUE = 10;

  public globalError: string | null = null;
  public toast: Notification | null = null;

  private destroyRef = inject(DestroyRef);
  private toastTimer?: ReturnType<typeof setTimeout>; // UPDATED: Strict typing instead of 'any'
  private nextToastTimer?: ReturnType<typeof setTimeout>;
  // FE-038: toasts queue up — a new toast must not silently replace an
  // unexpired one (and the old BehaviorSubject must not replay stale ones).
  private toastQueue: Notification[] = [];

  constructor(
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.notificationService.globalError$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((message) => {
        this.globalError = message;
        this.cdr.markForCheck(); // Inform Angular to update the view
      });

    this.notificationService.toastNotification$
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((notification) => {
        const isDuplicate =
          (this.toast?.type === notification.type &&
            this.toast?.message === notification.message) ||
          this.toastQueue.some(
            (item) => item.type === notification.type && item.message === notification.message,
          );
        if (!isDuplicate && this.toastQueue.length < NotificationHostComponent.MAX_TOAST_QUEUE) {
          this.toastQueue.push(notification);
          this.showNextToast();
        }
      });
  }

  // FE-038: display one toast at a time, FIFO, 4 s each.
  private showNextToast(): void {
    if (this.toast || this.nextToastTimer || this.toastQueue.length === 0) {
      return;
    }

    this.toast = this.toastQueue.shift() ?? null;
    this.cdr.markForCheck(); // Inform Angular to update the view

    this.toastTimer = setTimeout(() => {
      this.toastTimer = undefined;
      this.toast = null;
      this.cdr.markForCheck(); // Inform Angular to update the view when hiding
      this.scheduleNextToast();
    }, 4000);
  }

  private scheduleNextToast(): void {
    if (this.toastQueue.length === 0 || this.nextToastTimer) {
      return;
    }
    this.nextToastTimer = setTimeout(() => {
      this.nextToastTimer = undefined;
      this.showNextToast();
    }, 50);
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
    if (this.nextToastTimer) {
      clearTimeout(this.nextToastTimer);
      this.nextToastTimer = undefined;
    }
    this.toast = null;
    this.cdr.markForCheck();
    this.scheduleNextToast();
  }

  ngOnDestroy(): void {
    if (this.toastTimer) {
      clearTimeout(this.toastTimer);
    }
    if (this.nextToastTimer) {
      clearTimeout(this.nextToastTimer);
    }
    this.toastQueue = [];
  }
}
