import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormBuilder, FormGroup, ReactiveFormsModule } from '@angular/forms';
import { CustomField } from '../../../core/models';
import { buildCustomFieldsFormGroup } from '../../../shared/utils/custom-fields';
import { CustomFieldsFormComponent } from './custom-fields-form.component';

describe('CustomFieldsFormComponent', () => {
  let fixture: ComponentFixture<CustomFieldsFormComponent>;
  let component: CustomFieldsFormComponent;
  let fb: FormBuilder;
  let formGroup: FormGroup;

  const sampleFields: CustomField[] = [
    { id: 1, name: 'title', type: 'TEXT' },
    { id: 2, name: 'count', type: 'INTEGER' },
    { id: 3, name: 'ratio', type: 'REAL' },
    { id: 4, name: 'published', type: 'BOOLEAN' },
    { id: 5, name: 'location', type: 'COORDINATE' },
  ];

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [ReactiveFormsModule, CustomFieldsFormComponent],
    }).compileComponents();

    fb = TestBed.inject(FormBuilder);
    formGroup = fb.group({
      title: ['Hello'],
      count: [42],
      ratio: [3.14],
      published: [true],
      location: fb.group({
        latitude: [47.3769],
        longitude: [8.5417],
      }),
    });

    fixture = TestBed.createComponent(CustomFieldsFormComponent);
    component = fixture.componentInstance;
    component.fields = sampleFields;
    component.formGroup = formGroup;
    component.idPrefix = 'edit-';
    fixture.detectChanges();
  });

  it('renders all 5 custom field types with idPrefix and heading', () => {
    const el: HTMLElement = fixture.nativeElement;
    expect(el.querySelector('h4')?.textContent?.trim()).toBe('Custom Metadata');

    const textInput = el.querySelector('#edit-title') as HTMLInputElement;
    expect(textInput.type).toBe('text');
    expect(textInput.value).toBe('Hello');

    const intInput = el.querySelector('#edit-count') as HTMLInputElement;
    expect(intInput.type).toBe('number');
    expect(intInput.step).toBe('1');
    expect(intInput.value).toBe('42');

    const realInput = el.querySelector('#edit-ratio') as HTMLInputElement;
    expect(realInput.type).toBe('number');
    expect(realInput.step).toBe('any');
    expect(realInput.value).toBe('3.14');

    const boolInput = el.querySelector('#edit-published') as HTMLInputElement;
    expect(boolInput.type).toBe('checkbox');
    expect(boolInput.checked).toBeTrue();

    const coordInputs = el.querySelectorAll(
      '.coordinate-input-group input[type="number"]',
    ) as NodeListOf<HTMLInputElement>;
    expect(coordInputs.length).toBe(2);
    expect(coordInputs[0].min).toBe('-90');
    expect(coordInputs[0].max).toBe('90');
    expect(coordInputs[0].value).toBe('47.3769');
    expect(coordInputs[1].min).toBe('-180');
    expect(coordInputs[1].max).toBe('180');
    expect(coordInputs[1].value).toBe('8.5417');
  });

  it('updates form values when user interacts with inputs and toggle switch', () => {
    const el: HTMLElement = fixture.nativeElement;

    const textInput = el.querySelector('#edit-title') as HTMLInputElement;
    textInput.value = 'Updated';
    textInput.dispatchEvent(new Event('input'));

    const boolInput = el.querySelector('#edit-published') as HTMLInputElement;
    boolInput.click();
    fixture.detectChanges();

    expect(formGroup.get('title')?.value).toBe('Updated');
    expect(formGroup.get('published')?.value).toBeFalse();
  });

  it('renders custom heading and hides section when fields array is empty', () => {
    fixture.componentRef.setInput('heading', 'Extra Fields');
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('h4')?.textContent?.trim()).toBe('Extra Fields');

    fixture.componentRef.setInput('fields', []);
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('.custom-fields-section')).toBeNull();
  });

  it('renders .form-error messages for invalid INTEGER and COORDINATE controls', () => {
    const validatedGroup = buildCustomFieldsFormGroup(fb, sampleFields);
    component.formGroup = validatedGroup;
    fixture.detectChanges();

    expect(fixture.nativeElement.querySelectorAll('.form-error').length).toBe(0);

    validatedGroup.get('count')?.setValue(3.5);
    validatedGroup.get('location')?.patchValue({ latitude: 47.0, longitude: null });
    fixture.detectChanges();

    let errors = Array.from(fixture.nativeElement.querySelectorAll('.form-error')).map((el) =>
      (el as HTMLElement).textContent?.trim(),
    );
    expect(errors).toContain('Must be a whole number.');
    expect(errors).toContain('Enter both latitude and longitude.');

    validatedGroup.get('count')?.setValue(5);
    validatedGroup.get('location')?.patchValue({ latitude: 95, longitude: -185 });
    fixture.detectChanges();

    errors = Array.from(fixture.nativeElement.querySelectorAll('.form-error')).map((el) =>
      (el as HTMLElement).textContent?.trim(),
    );
    expect(errors).not.toContain('Must be a whole number.');
    expect(errors).not.toContain('Enter both latitude and longitude.');
    expect(errors).toContain('Latitude must be between -90 and 90.');
    expect(errors).toContain('Longitude must be between -180 and 180.');
  });
});
