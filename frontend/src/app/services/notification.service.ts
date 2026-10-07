// frontend/src/app/services/notification.service.ts

import { Injectable } from '@angular/core';
import { BehaviorSubject, Observable, Subject } from 'rxjs';

/**
 * Defines the structure for a notification message.
 * FE-038: `id` keys the display queue in NotificationHostComponent.
 */
export interface Notification {
  id: number;
  message: string;
  type: 'success' | 'error' | 'info';
}

/**
 * Manages the display of global error messages and success toast notifications.
 * This service provides observables for components to subscribe to.
 */
@Injectable({
  providedIn: 'root',
})
export class NotificationService {
  /**
   * BehaviorSubject for critical, global error banners.
   * e.g., "Server is down."
   */
  private globalErrorSubject = new BehaviorSubject<string | null>(null);

  /**
   * FE-038: a plain Subject of id-keyed toasts. The old single-slot
   * BehaviorSubject replayed the last toast to every new subscriber and silently
   * replaced an unexpired toast — the host now queues them (see
   * NotificationHostComponent).
   */
  private toastNotificationSubject = new Subject<Notification>();
  private nextToastId = 1;

  /**
   * Observable for components to listen for global errors.
   */
  public globalError$: Observable<string | null> = this.globalErrorSubject.asObservable();

  /**
   * Observable for components to listen for toast notifications.
   */
  public toastNotification$: Observable<Notification> =
    this.toastNotificationSubject.asObservable();

  constructor() {}

  /**
   * Shows a global error banner.
   * @param message The error message to display.
   */
  showGlobalError(message: string): void {
    this.globalErrorSubject.next(message);
  }

  /**
   * Clears the global error banner.
   */
  clearGlobalError(): void {
    this.globalErrorSubject.next(null);
  }

  /**
   * Shows a success toast notification.
   * @param message The success message to display.
   */
  showSuccess(message: string): void {
    this.enqueueToast(message, 'success');
  }

  /**
   * Shows an error toast notification.
   * @param message The error message to display.
   */
  showError(message: string): void {
    this.enqueueToast(message, 'error');
  }

  /**
   * Shows an info toast notification.
   * @param message The info message to display.
   */
  showInfo(message: string): void {
    this.enqueueToast(message, 'info');
  }

  private enqueueToast(message: string, type: Notification['type']): void {
    this.toastNotificationSubject.next({ id: this.nextToastId++, message, type });
  }
}
