// frontend/src/app/components/user-detail-form/user-detail-form.component.spec.ts

import { ComponentFixture, TestBed } from '@angular/core/testing';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { UserDetailFormComponent } from './user-detail-form.component';

describe('UserDetailFormComponent', () => {
  let fixture: ComponentFixture<UserDetailFormComponent>;
  let component: UserDetailFormComponent;
  let fb: FormBuilder;

  function buildForm(overrides?: Partial<{ username: string; account_type: string }>): FormGroup {
    return fb.group({
      id: [null],
      username: [overrides?.username ?? '', [Validators.required, Validators.maxLength(64)]],
      password: [''],
      is_admin: [false],
      account_type: [overrides?.account_type ?? 'local'],
      permissions: fb.array([
        fb.group({
          database_id: ['db1'],
          database_name: ['Music'],
          can_view: [false],
          can_create: [false],
          can_edit: [false],
          can_delete: [false],
          can_admin: [false],
        }),
      ]),
    });
  }

  const usernameError = (): HTMLElement | undefined =>
    (Array.from(fixture.nativeElement.querySelectorAll('.form-error')) as HTMLElement[]).find(
      (el) => (el.textContent ?? '').includes('Username is required'),
    );

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [UserDetailFormComponent],
    }).compileComponents();

    fb = TestBed.inject(FormBuilder);
    fixture = TestBed.createComponent(UserDetailFormComponent);
    component = fixture.componentInstance;
    component.formGroup = buildForm();
    fixture.detectChanges();
  });

  it('keeps username required/maxlength error hidden until touched or submitted, and displays it when invalid and submitted', () => {
    expect(usernameError()).toBeUndefined();

    // Submitted with empty username -> displays required/maxlength error
    component.submitted = true;
    fixture.detectChanges();
    expect(usernameError()?.textContent?.trim()).toBe(
      'Username is required and must be at most 64 characters.',
    );

    // Valid username clears the error
    component.formGroup.get('username')?.setValue('valid.user');
    fixture.detectChanges();
    expect(usernameError()).toBeUndefined();

    // Exceeding 64 chars shows the error when touched/submitted
    component.submitted = false;
    component.formGroup.get('username')?.markAsUntouched();
    component.formGroup.get('username')?.setValue('a'.repeat(65));
    fixture.detectChanges();
    expect(usernameError()).toBeUndefined();

    component.submitted = true;
    fixture.detectChanges();
    expect(usernameError()?.textContent?.trim()).toBe(
      'Username is required and must be at most 64 characters.',
    );
  });

  it('shows password input for local accounts, SSO text for oidc, and hides password section for service_account', () => {
    // Local account -> password input visible
    component.formGroup = buildForm({ username: 'alice', account_type: 'local' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('input[type="password"]')).toBeTruthy();

    // OIDC account -> no password input, shows SSO managed text
    component.formGroup = buildForm({ username: 'john.doe', account_type: 'oidc' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('input[type="password"]')).toBeNull();
    expect(fixture.nativeElement.textContent).toContain(
      'Managed via Single Sign-On Identity Provider',
    );

    // Service account -> no password input, no SSO text
    component.formGroup = buildForm({ username: 'ci-bot', account_type: 'service_account' });
    fixture.detectChanges();
    expect(fixture.nativeElement.querySelector('input[type="password"]')).toBeNull();
    expect(fixture.nativeElement.textContent).not.toContain(
      'Managed via Single Sign-On Identity Provider',
    );
  });

  it('emits toggleColumn when clicking "Toggle All" and emits save when submitting the form', () => {
    component.formGroup = buildForm({ username: 'alice', account_type: 'local' });
    fixture.detectChanges();

    const toggleSpy = spyOn(component.toggleColumn, 'emit');
    const saveSpy = spyOn(component.save, 'emit');

    const toggleBtn = (
      Array.from(fixture.nativeElement.querySelectorAll('button.btn-link')) as HTMLButtonElement[]
    ).find((btn) => (btn.textContent ?? '').includes('Toggle All'));
    expect(toggleBtn).toBeTruthy();
    toggleBtn!.click();
    expect(toggleSpy).toHaveBeenCalledOnceWith('can_view');

    const form = fixture.nativeElement.querySelector('form') as HTMLFormElement;
    form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    expect(saveSpy).toHaveBeenCalledTimes(1);
  });
});
