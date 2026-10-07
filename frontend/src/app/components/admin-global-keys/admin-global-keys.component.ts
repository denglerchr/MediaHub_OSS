// frontend/src/app/components/admin-global-keys/admin-global-keys.component.ts

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { Subject } from 'rxjs';
import { takeUntil, finalize, filter, take } from 'rxjs/operators';
import { ApiKey } from '../../models';
import { AuthService } from '../../services/auth.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import {
  ConfirmationModalComponent,
  ConfirmationModalData,
} from '../confirmation-modal/confirmation-modal.component';
import { ApiKeyModalComponent } from '../api-key-modal/api-key-modal.component';

@Component({
  selector: 'app-admin-global-keys',
  templateUrl: './admin-global-keys.component.html',
  styleUrls: ['./admin-global-keys.component.css'],
  standalone: false,
})
export class AdminGlobalKeysComponent implements OnInit, OnDestroy {
  public apiKeys: ApiKey[] = [];
  public isLoading = true;

  private destroy$ = new Subject<void>();

  constructor(
    private authService: AuthService,
    private modalService: ModalService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.loadGlobalKeys();
  }

  public loadGlobalKeys(): void {
    this.isLoading = true;
    this.cdr.markForCheck();

    this.authService
      .getGlobalKeys()
      .pipe(
        takeUntil(this.destroy$),
        finalize(() => {
          this.isLoading = false;
          this.cdr.markForCheck();
        }),
      )
      .subscribe({
        next: (keys) => {
          this.apiKeys = keys || [];
          this.cdr.markForCheck();
        },
        error: (err) => {
          console.error('Failed to load global API keys', err);
          this.notificationService.showError('Could not load global API keys.');
          this.cdr.markForCheck();
        },
      });
  }

  public openRevokeConfirm(key: ApiKey): void {
    const ownerId = key.user?.id;
    if (!ownerId) {
      this.notificationService.showError('Could not identify key owner.');
      return;
    }

    const ownerName = key.user?.username || 'Unknown User';
    const modalData: ConfirmationModalData = {
      title: 'Revoke Key Globally',
      message: `Are you sure you want to revoke the key "${key.name}" belonging to user "${ownerName}"? This action is immediate and cannot be undone.`,
    };

    this.modalService
      .open(ConfirmationModalComponent.MODAL_ID, modalData)
      .pipe(
        take(1),
        filter((isConfirmed) => isConfirmed === true),
      )
      .subscribe(() => {
        this.authService
          .deleteUserKey(ownerId, key.id)
          .pipe(takeUntil(this.destroy$))
          .subscribe({
            next: () => {
              this.notificationService.showSuccess('API key revoked successfully.');
              this.loadGlobalKeys();
            },
            error: () => {
              // FE-046: the shared AuthService.handleError already surfaced the reason.
              this.cdr.markForCheck();
            },
          });
      });
  }

  trackByKeyId(index: number, key: ApiKey): string {
    return key.id;
  }

  public openEditKeyModal(key: ApiKey): void {
    const ownerId = key.user?.id;
    if (!ownerId) {
      this.notificationService.showError('Could not identify key owner.');
      return;
    }

    this.modalService
      .open(ApiKeyModalComponent.MODAL_ID, { userId: ownerId, apiKey: key })
      .pipe(take(1))
      .subscribe((updated) => {
        if (updated) {
          this.loadGlobalKeys();
        }
      });
  }

  ngOnDestroy(): void {
    this.destroy$.next();
    this.destroy$.complete();
  }
}
