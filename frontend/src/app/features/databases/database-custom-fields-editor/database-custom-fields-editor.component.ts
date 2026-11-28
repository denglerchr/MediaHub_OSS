import { CommonModule } from '@angular/common';
import { Component, DestroyRef, Input, OnChanges, SimpleChanges, inject } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { filter, finalize } from 'rxjs/operators';
import {
  ConfirmationModalData,
  CustomField,
  CustomFieldType,
  Database,
} from '../../../core/models';
import { DatabaseService } from '../../../core/services/database.service';
import { ModalService } from '../../../core/services/modal.service';
import { NotificationService } from '../../../core/services/notification.service';
import { validateCustomFieldName } from '../../../shared/utils/validation';

@Component({
  selector: 'app-database-custom-fields-editor',
  standalone: true,
  imports: [CommonModule, FormsModule],
  templateUrl: './database-custom-fields-editor.component.html',
  styleUrls: ['./database-custom-fields-editor.component.css'],
})
export class DatabaseCustomFieldsEditorComponent implements OnChanges {
  @Input() database!: Database;
  @Input() canAdmin = false;

  public newFieldName = '';
  public newFieldType: CustomFieldType = 'TEXT';
  public newFieldIsIndexed = true;

  public editingFieldId: number | null = null;
  public editingFieldName = '';
  public editingFieldIsIndexed = false;

  public isMutatingField = false;

  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private databaseService: DatabaseService,
    private modalService: ModalService,
    private notificationService: NotificationService,
  ) {}

  ngOnChanges(changes: SimpleChanges): void {
    if (
      changes['database'] &&
      changes['database'].previousValue?.id !== changes['database'].currentValue?.id
    ) {
      this.editingFieldId = null;
      this.newFieldName = '';
      this.newFieldType = 'TEXT';
      this.newFieldIsIndexed = false;
    }
    if (!this.canAdmin) {
      this.editingFieldId = null;
    }
  }

  private validateCustomFieldName(name: string, excludeFieldId?: number): string | null {
    const existingNames = (this.database?.custom_fields ?? [])
      .filter((f) => f.id !== excludeFieldId)
      .map((f) => f.name);
    return validateCustomFieldName(name, existingNames);
  }

  onAddField(): void {
    if (this.isMutatingField || !this.database || !this.canAdmin) return;

    const trimmedName = this.newFieldName.trim();
    if (!trimmedName) return;

    const validationError = this.validateCustomFieldName(trimmedName);
    if (validationError) {
      this.notificationService.showError(validationError);
      return;
    }

    this.isMutatingField = true;
    const targetDbId = this.database.id;
    const newField: CustomField = {
      name: trimmedName,
      type: this.newFieldType,
      is_indexed: this.newFieldIsIndexed,
    };

    this.databaseService
      .addCustomField(targetDbId, newField)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => (this.isMutatingField = false)),
      )
      .subscribe({
        next: () => {
          if (this.database?.id !== targetDbId) {
            return;
          }
          this.newFieldName = '';
          this.newFieldType = 'TEXT';
          this.newFieldIsIndexed = true;
        },
        error: () => {},
      });
  }

  onStartEditField(field: CustomField): void {
    if (field.id === undefined) return;
    this.editingFieldId = field.id;
    this.editingFieldName = field.name;
    this.editingFieldIsIndexed = field.is_indexed || false;
  }

  onCancelEditField(): void {
    this.editingFieldId = null;
  }

  onSaveField(fieldId: number): void {
    if (this.isMutatingField || !this.database || !this.canAdmin) return;

    const trimmedName = this.editingFieldName.trim();
    if (!trimmedName) return;

    const validationError = this.validateCustomFieldName(trimmedName, fieldId);
    if (validationError) {
      this.notificationService.showError(validationError);
      return;
    }

    this.isMutatingField = true;
    const targetDbId = this.database.id;
    this.databaseService
      .updateCustomField(targetDbId, fieldId, trimmedName, this.editingFieldIsIndexed)
      .pipe(
        takeUntilDestroyed(this.destroyRef),
        finalize(() => {
          this.isMutatingField = false;
        }),
      )
      .subscribe({
        next: () => {
          if (this.database?.id !== targetDbId || this.editingFieldId !== fieldId) {
            return;
          }
          this.editingFieldId = null;
        },
        error: () => {},
      });
  }

  onDeleteField(field: CustomField): void {
    if (this.isMutatingField || !this.database || !this.canAdmin || field.id === undefined) {
      return;
    }

    const dbId = this.database.id;
    const dbName = this.database.name;
    const fieldId = field.id;

    const modalData: ConfirmationModalData = {
      title: 'Confirm Custom Field Deletion',
      message: `This action will permanently delete the custom field '${field.name}' from database '${dbName}' and discard all entry data stored under this field. This action cannot be undone.`,
    };

    this.modalService
      .confirm(modalData)
      .pipe(filter(Boolean), takeUntilDestroyed(this.destroyRef))
      .subscribe(() => {
        this.isMutatingField = true;
        this.databaseService
          .deleteCustomField(dbId, fieldId)
          .pipe(
            takeUntilDestroyed(this.destroyRef),
            finalize(() => (this.isMutatingField = false)),
          )
          .subscribe({
            error: () => {},
          });
      });
  }

  trackByFieldId(index: number, field: CustomField): number | string {
    return field.id ?? field.name;
  }
}
