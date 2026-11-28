import { CommonModule } from '@angular/common';
import {
  Component,
  Input,
  Output,
  EventEmitter,
  OnChanges,
  SimpleChanges,
  ChangeDetectionStrategy,
  ChangeDetectorRef,
  DestroyRef,
  OnInit,
  OnDestroy,
  inject,
} from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import {
  FormBuilder,
  FormGroup,
  FormArray,
  Validators,
  AbstractControl,
  ReactiveFormsModule,
} from '@angular/forms';
import { Subject } from 'rxjs';
import { takeUntil } from 'rxjs/operators';
import { SearchFilter, SearchCondition, CustomFieldType } from '../../../core/models';
import { EntryStatus } from '../../../core/models/enums';
import { DatetimeDefaultDirective } from '../../../shared/directives/datetime-default.directive';
import { coordinateBoxValidator } from '../../../shared/utils/coordinates';
import { parseDatetimeLocalToMs } from '../../../shared/utils/datetime';

export interface FilterChangedEvent {
  filter: SearchFilter | undefined;
  limit: number;
}

export interface AvailableFilter {
  name: string;
  type: CustomFieldType | 'STATUS';
}

/**
 * FE-027/FE-033: precomputed per-row state. The template used to call
 * `getSelectedFieldType(i)` three times per row per change-detection cycle (each a
 * linear scan over availableFilters) and `activeFiltersCount` was a getter
 * recomputed per cycle — both are now maintained on value changes instead.
 * `destroy$` scopes the row's `field.valueChanges` subscription so a removed row
 * (FE-033) does not leak until component destruction.
 */
interface FilterRowMeta {
  fieldType: string | null;
  operators: string[];
  destroy$: Subject<void>;
}

@Component({
  selector: 'app-entry-filter',
  templateUrl: './entry-filter.component.html',
  styleUrls: ['./entry-filter.component.css'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: true,
  imports: [CommonModule, ReactiveFormsModule, DatetimeDefaultDirective],
})
export class EntryFilterComponent implements OnInit, OnChanges, OnDestroy {
  @Input() availableFilters: AvailableFilter[] = [];
  @Input() isLoading = false;

  @Output() filterApplied = new EventEmitter<FilterChangedEvent>();

  public filterForm: FormGroup;
  public isCollapsed = true;

  // FE-027: precomputed per-row state (kept in sync with the customFilters FormArray).
  public filterRows: FilterRowMeta[] = [];
  // FE-027: precomputed active-filter count (was a per-cycle getter).
  public activeFiltersCount = 0;

  private readonly destroyRef = inject(DestroyRef);

  constructor(
    private fb: FormBuilder,
    private cdr: ChangeDetectorRef,
  ) {
    this.filterForm = this.fb.group({
      limitPerPage: [
        200,
        [Validators.required, Validators.min(1), Validators.max(999), Validators.pattern(/^\d+$/)],
      ],
      tstart: [''],
      tend: [''],
      customFilters: this.fb.array([]),
    });
  }

  ngOnInit(): void {
    // Automatically re-evaluate the active filters count if the form changes
    this.filterForm.valueChanges.pipe(takeUntilDestroyed(this.destroyRef)).subscribe(() => {
      this.recomputeActiveFilters();
      this.cdr.markForCheck();
    });
  }

  toggleCollapse(): void {
    this.isCollapsed = !this.isCollapsed;
    this.cdr.markForCheck();
  }

  /**
   * FE-027: how many filter conditions are actively set. Recomputed on every form
   * change instead of being derived per change-detection cycle.
   */
  private recomputeActiveFilters(): void {
    let count = 0;
    const formValue = this.filterForm.value;

    if (formValue.tstart) count++;
    if (formValue.tend) count++;

    formValue.customFilters.forEach((filter: { field?: string; value?: unknown }) => {
      // Only count custom filters that actually have a field and a value selected
      if (filter.field && filter.value !== null && filter.value !== undefined) {
        if (
          typeof filter.value === 'object' &&
          filter.value !== null &&
          'min_lat' in filter.value
        ) {
          const { min_lat, max_lat, min_lng, max_lng } = filter.value as Record<string, unknown>;
          if (
            min_lat !== '' &&
            max_lat !== '' &&
            min_lng !== '' &&
            max_lng !== '' &&
            min_lat !== null &&
            max_lat !== null &&
            min_lng !== null &&
            max_lng !== null
          ) {
            count++;
          }
        } else if (String(filter.value).trim() !== '') {
          count++;
        }
      }
    });

    this.activeFiltersCount = count;
  }

  ngOnChanges(changes: SimpleChanges): void {
    if (changes['availableFilters'] && !changes['availableFilters'].firstChange) {
      this.clearAllFilters();
      this.isCollapsed = true;
      this.cdr.markForCheck();
    }
  }

  get customFilters(): FormArray {
    return this.filterForm.get('customFilters') as FormArray;
  }

  addCustomFilter(): void {
    const newGroup = this.fb.group({
      field: ['', Validators.required],
      operator: ['='],
      value: this.fb.control<unknown>('', Validators.required),
    });

    // FE-033: per-row teardown scope — completed in removeCustomFilter/clearAllFilters.
    const meta: FilterRowMeta = { fieldType: null, operators: [], destroy$: new Subject<void>() };

    newGroup
      .get('field')
      ?.valueChanges.pipe(takeUntil(meta.destroy$))
      .subscribe(() => {
        const index = this.customFilters.controls.indexOf(newGroup);
        if (index === -1) return;
        const rowMeta = this.filterRows[index];

        const fieldType = this.getSelectedFieldTypeForGroup(newGroup);
        rowMeta.fieldType = fieldType;
        rowMeta.operators = this.getOperatorsForFieldType(fieldType);

        const defaultOp = rowMeta.operators[0] || '=';
        newGroup.get('operator')?.setValue(defaultOp);

        const valueControl = newGroup.get('value');
        if (fieldType === 'COORDINATE') {
          valueControl?.setValidators([coordinateBoxValidator]);
          valueControl?.setValue({ min_lat: '', max_lat: '', min_lng: '', max_lng: '' });
        } else {
          if (fieldType === 'INTEGER') {
            valueControl?.setValidators([Validators.required, Validators.pattern(/^-?\d+$/)]);
          } else {
            valueControl?.setValidators([Validators.required]);
          }
          if (fieldType === 'BOOLEAN') {
            valueControl?.setValue('true');
          } else if (fieldType === 'STATUS') {
            valueControl?.setValue(EntryStatus.Ready);
          } else {
            valueControl?.setValue('');
          }
        }
        valueControl?.updateValueAndValidity();
      });

    this.customFilters.push(newGroup);
    this.filterRows.push(meta);

    // Automatically expand the panel if a user clicks "+ Add Filter" programmatically
    // or if a filter is added via another method
    if (this.isCollapsed) {
      this.isCollapsed = false;
    }
  }

  removeCustomFilter(index: number): void {
    // FE-033: the removed row's `field.valueChanges` subscription must die with the
    // row — it used to leak until component destruction.
    const meta = this.filterRows[index];
    if (meta) {
      meta.destroy$.next();
      meta.destroy$.complete();
      this.filterRows.splice(index, 1);
    }
    this.customFilters.removeAt(index);
    this.recomputeActiveFilters();
    this.cdr.markForCheck();
  }

  /** Drops every filter row (and its subscription) plus the time range. */
  private clearAllFilters(): void {
    this.filterRows.forEach((meta) => {
      meta.destroy$.next();
      meta.destroy$.complete();
    });
    this.filterRows = [];
    this.customFilters.clear();
    this.filterForm.patchValue({ limitPerPage: 200, tstart: '', tend: '' });
    this.recomputeActiveFilters();
  }

  applyFilters(): void {
    if (this.filterForm.invalid) {
      this.filterForm.markAllAsTouched();
      return;
    }

    const event = this.buildFilterEvent();
    this.filterApplied.emit(event);

    // Auto-collapse on mobile after applying to get the filter out of the way of the results
    if (window.innerWidth < 768) {
      this.isCollapsed = true;
    }
  }

  private buildFilterEvent(): FilterChangedEvent {
    const formValue = this.filterForm.value;
    const conditions: SearchCondition[] = [];

    // 1. Time Filters
    if (formValue.tstart) {
      const tstartUnix = parseDatetimeLocalToMs(formValue.tstart);
      if (tstartUnix !== null) {
        conditions.push({ field: 'timestamp', operator: '>=', value: tstartUnix });
      }
    }
    if (formValue.tend) {
      const tendUnix = parseDatetimeLocalToMs(formValue.tend, { endOfMinute: true });
      if (tendUnix !== null) {
        conditions.push({ field: 'timestamp', operator: '<=', value: tendUnix });
      }
    }

    // 2. Custom Filters
    formValue.customFilters.forEach(
      (filter: { field?: string; operator: string; value?: unknown }) => {
        if (filter.field && filter.value !== null && filter.value !== undefined) {
          const fieldDefinition = this.availableFilters.find((f) => f.name === filter.field);
          let filterValue: unknown = filter.value;

          if (fieldDefinition) {
            if (fieldDefinition.type === 'COORDINATE' && filter.operator === 'in_box') {
              if (filterValue && typeof filterValue === 'object') {
                const { min_lat, max_lat, min_lng, max_lng } = filterValue as Record<
                  string,
                  unknown
                >;
                if (
                  min_lat !== '' &&
                  max_lat !== '' &&
                  min_lng !== '' &&
                  max_lng !== '' &&
                  min_lat !== null &&
                  max_lat !== null &&
                  min_lng !== null &&
                  max_lng !== null
                ) {
                  const numMinLat = Number(min_lat);
                  const numMaxLat = Number(max_lat);
                  const numMinLng = Number(min_lng);
                  const numMaxLng = Number(max_lng);
                  if (
                    Number.isFinite(numMinLat) &&
                    Number.isFinite(numMaxLat) &&
                    Number.isFinite(numMinLng) &&
                    Number.isFinite(numMaxLng)
                  ) {
                    filterValue = {
                      min_lat: numMinLat,
                      max_lat: numMaxLat,
                      min_lng: numMinLng,
                      max_lng: numMaxLng,
                    };
                  } else {
                    filterValue = null;
                  }
                } else {
                  filterValue = null;
                }
              } else {
                filterValue = null;
              }
            } else {
              filterValue = String(filterValue).trim();
              if (filterValue === '') {
                filterValue = null;
              } else if (fieldDefinition.type === 'INTEGER' || fieldDefinition.type === 'REAL') {
                const num = Number(filterValue);
                if (!isNaN(num)) {
                  filterValue = num;
                }
              } else if (fieldDefinition.type === 'BOOLEAN') {
                const lowerVal = String(filterValue).toLowerCase();
                if (lowerVal === 'true' || lowerVal === '1') {
                  filterValue = true;
                } else if (lowerVal === 'false' || lowerVal === '0') {
                  filterValue = false;
                }
              } else if (fieldDefinition.type === 'TEXT' && filter.operator === 'LIKE') {
                filterValue = `%${filterValue}%`;
              }
            }
          }

          if (filterValue !== null) {
            conditions.push({
              field: filter.field,
              operator: filter.operator,
              value: filterValue,
            });
          }
        }
      },
    );

    let filterObject: SearchFilter | undefined;
    if (conditions.length > 0) {
      filterObject = {
        operator: 'and',
        conditions: conditions,
      };
    }

    return {
      filter: filterObject,
      limit: formValue.limitPerPage,
    };
  }

  getOperatorsForFieldType(fieldType: string | null | undefined): string[] {
    switch (fieldType) {
      case 'INTEGER':
      case 'REAL':
        return ['=', '!=', '>', '>=', '<', '<='];
      case 'BOOLEAN':
      case 'STATUS':
        return ['=', '!='];
      case 'TEXT':
        return ['=', '!=', 'LIKE'];
      case 'COORDINATE':
        return ['in_box'];
      default:
        return ['=', '!='];
    }
  }

  trackByRow(_index: number, control: AbstractControl): AbstractControl {
    return control;
  }

  trackByFieldName(_index: number, field: AvailableFilter): string {
    return field.name;
  }

  trackByOperator(_index: number, op: string): string {
    return op;
  }

  getBboxCoord(index: number, key: 'min_lat' | 'max_lat' | 'min_lng' | 'max_lng'): string | number {
    const val = this.customFilters.at(index)?.get('value')?.value;
    return val && typeof val === 'object' && val[key] !== undefined && val[key] !== null
      ? val[key]
      : '';
  }

  setBboxCoord(
    index: number,
    key: 'min_lat' | 'max_lat' | 'min_lng' | 'max_lng',
    event: Event,
  ): void {
    const input = event.target as HTMLInputElement;
    const valueControl = this.customFilters.at(index)?.get('value');
    const current = valueControl?.value || {};
    const trimmed = input.value.trim();
    const updated = {
      ...current,
      [key]: trimmed === '' ? null : trimmed,
    };
    valueControl?.setValue(updated);
    valueControl?.markAsTouched();
  }

  private getSelectedFieldTypeForGroup(group: AbstractControl | null): string | null {
    if (!group) return null;
    const fieldName = group.get('field')?.value;
    const fieldDefinition = this.availableFilters.find((f) => f.name === fieldName);
    return fieldDefinition ? fieldDefinition.type : null;
  }

  ngOnDestroy(): void {
    // FE-033: tear down per-row subscriptions too.
    this.filterRows.forEach((meta) => {
      meta.destroy$.next();
      meta.destroy$.complete();
    });
    this.filterRows = [];
  }
}
