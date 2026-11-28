import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { ToggleSwitchComponent } from './toggle-switch.component';

@Component({
  standalone: true,
  imports: [ReactiveFormsModule, ToggleSwitchComponent],
  template: `
    <app-toggle-switch
      [formControl]="control"
      [inputId]="inputId"
      [trueLabel]="trueLabel"
      [falseLabel]="falseLabel"
      [showLabel]="showLabel"
    ></app-toggle-switch>
  `,
})
class TestHostComponent {
  control = new FormControl(false, { nonNullable: true });
  inputId = 'test-toggle';
  trueLabel = 'True';
  falseLabel = 'False';
  showLabel = true;
}

describe('ToggleSwitchComponent', () => {
  let fixture: ComponentFixture<TestHostComponent>;
  let host: TestHostComponent;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [TestHostComponent],
    }).compileComponents();

    fixture = TestBed.createComponent(TestHostComponent);
    host = fixture.componentInstance;
    fixture.detectChanges();
  });

  function getCheckbox(): HTMLInputElement {
    return fixture.nativeElement.querySelector('input[type="checkbox"]') as HTMLInputElement;
  }

  function getLabelText(): string | null {
    const span = fixture.nativeElement.querySelector('.bool-field-value') as HTMLElement | null;
    return span ? (span.textContent?.trim() ?? null) : null;
  }

  it('reflects FormControl writeValue and toggles FormControl on UI change', () => {
    expect(getCheckbox().checked).toBeFalse();
    expect(getLabelText()).toBe('False');
    expect(getCheckbox().id).toBe('test-toggle');

    host.control.setValue(true);
    fixture.detectChanges();
    expect(getCheckbox().checked).toBeTrue();
    expect(getLabelText()).toBe('True');

    getCheckbox().click();
    fixture.detectChanges();
    expect(host.control.value).toBeFalse();
    expect(host.control.touched).toBeTrue();
    expect(getLabelText()).toBe('False');
  });

  it('supports disabled state via FormControl', () => {
    host.control.disable();
    fixture.detectChanges();
    expect(getCheckbox().disabled).toBeTrue();

    host.control.enable();
    fixture.detectChanges();
    expect(getCheckbox().disabled).toBeFalse();
  });

  it('renders custom labels and hides label when showLabel is false', () => {
    host.trueLabel = 'Enabled';
    host.falseLabel = 'Disabled';
    fixture.detectChanges();
    expect(getLabelText()).toBe('Disabled');

    host.control.setValue(true);
    fixture.detectChanges();
    expect(getLabelText()).toBe('Enabled');

    host.showLabel = false;
    fixture.detectChanges();
    expect(getLabelText()).toBeNull();
  });

  it('exposes switch ARIA role/state and ignores change events when disabled', () => {
    const checkbox = getCheckbox();
    expect(checkbox.getAttribute('role')).toBe('switch');
    expect(checkbox.getAttribute('aria-checked')).toBe('false');

    host.control.disable();
    fixture.detectChanges();

    checkbox.checked = true;
    checkbox.dispatchEvent(new Event('change'));
    fixture.detectChanges();

    expect(host.control.value).toBeFalse();
    expect(checkbox.getAttribute('aria-checked')).toBe('false');
  });
});
