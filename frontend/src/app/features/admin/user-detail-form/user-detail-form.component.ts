// frontend/src/app/components/user-detail-form/user-detail-form.component.ts

import { CommonModule } from '@angular/common';
import { Component, EventEmitter, Input, Output } from '@angular/core';
import { AbstractControl, FormArray, FormGroup, ReactiveFormsModule } from '@angular/forms';

export type PermissionColumn = 'can_view' | 'can_create' | 'can_edit' | 'can_delete' | 'can_admin';

@Component({
  selector: 'app-user-detail-form',
  templateUrl: './user-detail-form.component.html',
  styleUrls: ['./user-detail-form.component.css'],
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule],
})
export class UserDetailFormComponent {
  @Input() formGroup!: FormGroup;
  @Input() isNewUser = false;
  @Input() isSaving = false;
  @Input() submitted = false;

  @Output() save = new EventEmitter<void>();
  @Output() toggleColumn = new EventEmitter<PermissionColumn>();
  @Output() cancelled = new EventEmitter<void>();

  get permissions(): FormArray {
    return this.formGroup.get('permissions') as FormArray;
  }

  trackByDbId(index: number, control: AbstractControl): string | number {
    return control?.get ? (control.get('database_id')?.value ?? index) : index;
  }
}
