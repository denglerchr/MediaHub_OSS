import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormBuilder, FormGroup, ReactiveFormsModule, Validators } from '@angular/forms';
import { HOUSEKEEPING_INTEGER_PATTERN } from '../../../shared/utils/housekeeping';
import { HousekeepingFieldsComponent } from './housekeeping-fields.component';

describe('HousekeepingFieldsComponent', () => {
  let fixture: ComponentFixture<HousekeepingFieldsComponent>;
  let component: HousekeepingFieldsComponent;
  let formGroup: FormGroup;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ReactiveFormsModule, HousekeepingFieldsComponent],
    }).compileComponents();

    const fb = TestBed.inject(FormBuilder);
    const validators = [
      Validators.required,
      Validators.min(0),
      Validators.pattern(HOUSEKEEPING_INTEGER_PATTERN),
    ];
    formGroup = fb.group({
      interval_value: [1, validators],
      interval_unit: ['h'],
      disk_space_value: [100, validators],
      disk_space_unit: ['G'],
      max_age_value: [0, validators],
      max_age_unit: ['d'],
    });

    fixture = TestBed.createComponent(HousekeepingFieldsComponent);
    component = fixture.componentInstance;
    component.formGroup = formGroup;
    fixture.detectChanges();
  });

  it('renders labels, unit selects, and toggles helper icons', () => {
    const el: HTMLElement = fixture.nativeElement;
    const labels = Array.from(el.querySelectorAll('label.form-label')).map((l) =>
      l.textContent?.replace('?', '').trim(),
    );
    expect(labels).toEqual(['Run Interval', 'Max Disk Space', 'Max Entry Age']);
    expect(el.querySelectorAll('select').length).toBe(3);
    expect(el.querySelectorAll('.helper-icon').length).toBe(0);

    fixture.componentRef.setInput('showHelperIcons', true);
    fixture.detectChanges();
    expect(el.querySelectorAll('.helper-icon').length).toBe(2);
  });

  it('supports readOnly and showUnitSelect inputs', () => {
    fixture.componentRef.setInput('readOnly', true);
    fixture.componentRef.setInput('showUnitSelect', false);
    fixture.detectChanges();

    const el: HTMLElement = fixture.nativeElement;
    const inputs = Array.from(el.querySelectorAll('input[type="number"]')) as HTMLInputElement[];
    expect(inputs.length).toBe(3);
    expect(inputs.every((input) => input.readOnly)).toBeTrue();
    expect(el.querySelectorAll('select').length).toBe(0);
  });

  it('displays required, min, and pattern validation errors when touched', () => {
    const el: HTMLElement = fixture.nativeElement;
    const intervalCtrl = formGroup.get('interval_value')!;
    const diskCtrl = formGroup.get('disk_space_value')!;
    const ageCtrl = formGroup.get('max_age_value')!;

    intervalCtrl.setValue(null);
    intervalCtrl.markAsTouched();
    diskCtrl.setValue(-5);
    diskCtrl.markAsTouched();
    ageCtrl.setValue(1.5);
    ageCtrl.markAsTouched();
    fixture.detectChanges();

    const errorsText = Array.from(el.querySelectorAll('.form-error span')).map((node) =>
      node.textContent?.trim(),
    );
    expect(errorsText).toContain('Run interval is required.');
    expect(errorsText).toContain('Max disk space cannot be negative.');
    expect(errorsText).toContain('Max entry age must be a whole number.');
  });

  it('renders parseWarning when provided', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('.form-error')).toBeNull();

    fixture.componentRef.setInput('parseWarning', 'Unable to parse server housekeeping rule.');
    fixture.detectChanges();

    const errors = Array.from(el.querySelectorAll('.form-error')).map((e) => e.textContent?.trim());
    expect(errors).toContain('Unable to parse server housekeeping rule.');
  });
});
