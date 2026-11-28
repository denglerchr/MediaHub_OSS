// frontend/src/app/components/admin-global-keys/admin-global-keys.component.ts

import { CommonModule } from '@angular/common';
import { Component, OnInit, OnDestroy, ChangeDetectorRef, DestroyRef, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { Subject, of } from 'rxjs';
import { catchError, finalize, switchMap } from 'rxjs/operators';
import { ApiKey, ConfirmationModalData } from '../../../core/models';
import { ApiKeyService } from '../../../core/services/api-key.service';
import { NotificationService } from '../../../core/services/notification.service';
import { ApiKeyTableComponent } from '../api-key-table/api-key-table.component';
import { PageHeaderComponent } from '../../../shared/components/page-header/page-header.component';

@Component({
  selector: 'app-admin-global-keys',
  templateUrl: './admin-global-keys.component.html',
  styleUrls: ['./admin-global-keys.component.css'],
  standalone: true,
  imports: [CommonModule, PageHeaderComponent, ApiKeyTableComponent],
})
export class AdminGlobalKeysComponent implements OnInit, OnDestroy {
  public apiKeys: ApiKey[] = [];
  public isLoading = true;

  private readonly destroyRef = inject(DestroyRef);
  private loadKeysTrigger$ = new Subject<void>();

  constructor(
    private apiKeyService: ApiKeyService,
    private notificationService: NotificationService,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.loadKeysTrigger$
      .pipe(
        switchMap(() => {
          this.isLoading = true;
          this.cdr.markForCheck();
          return this.apiKeyService.getGlobalKeys().pipe(
            catchError((err) => {
              console.error('Failed to load global API keys', err);
              this.notificationService.showError('Could not load global API keys.');
              return of(null);
            }),
            finalize(() => {
              this.isLoading = false;
              this.cdr.markForCheck();
            }),
          );
        }),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe((keys) => {
        if (keys !== null) {
          this.apiKeys = keys || [];
          this.cdr.markForCheck();
        }
      });

    this.loadGlobalKeys();
  }

  public loadGlobalKeys(): void {
    this.loadKeysTrigger$.next();
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

    this.apiKeyService
      .confirmRevokeKey(ownerId, key, modalData)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((revoked) => {
        if (revoked) {
          this.loadGlobalKeys();
        } else {
          this.cdr.markForCheck();
        }
      });
  }

  public openEditKeyModal(key: ApiKey): void {
    const ownerId = key.user?.id;
    if (!ownerId) {
      this.notificationService.showError('Could not identify key owner.');
      return;
    }

    this.apiKeyService
      .openEditKeyModal(ownerId, key)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((updated) => {
        if (updated) {
          this.loadGlobalKeys();
        }
      });
  }

  ngOnDestroy(): void {
    this.loadKeysTrigger$.complete();
  }
}
