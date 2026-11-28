// frontend/src/app/components/api-key-table/api-key-table.component.ts

import { Component, EventEmitter, Input, OnChanges, Output } from '@angular/core';
import { CommonModule } from '@angular/common';
import { ApiKey } from '../../../core/models';
import { AccountTypeLabelPipe } from '../../../shared/pipes/account-type-label.pipe';

@Component({
  selector: 'app-api-key-table',
  templateUrl: './api-key-table.component.html',
  styleUrls: ['./api-key-table.component.css'],
  standalone: true,
  imports: [CommonModule, AccountTypeLabelPipe],
})
export class ApiKeyTableComponent implements OnChanges {
  @Input() keys: ApiKey[] = [];
  @Input() showOwner = false;
  @Input() emptyMessage = 'No API keys found.';
  @Input() revokeTitle = 'Revoke API Key';

  @Output() editKey = new EventEmitter<ApiKey>();
  @Output() revokeKey = new EventEmitter<ApiKey>();

  public now = Date.now();

  ngOnChanges(): void {
    this.now = Date.now();
  }

  public hasAnyScope(key: ApiKey): boolean {
    return !!(
      key.scope_view ||
      key.scope_create ||
      key.scope_edit ||
      key.scope_delete ||
      key.scope_admin
    );
  }

  public isExpired(key: ApiKey): boolean {
    return key.expires_at != null && key.expires_at < this.now;
  }

  public trackByKeyId(index: number, key: ApiKey): string {
    return key.id;
  }
}
