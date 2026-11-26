// Regression spec for FE-001 (AI/Reports/20261006_frontend_review.md):
// "User editing is impossible for usernames outside [a-zA-Z0-9_] (all dotted OIDC users)".
//
// OIDC users are JIT-provisioned from IdP claims and keep the IdP's preferred_username
// verbatim (see internal/repository/oidc.go ExtractUsernameBase and the "john.doe" fixture
// in internal/httpserver/tokenhandler/tokenhandler_test.go). The username validator in this
// component used to reject such names, which invalidated the whole form (username shares the
// form with the permissions FormArray) and silently disabled/dispatched nothing on save.
//
// Phase B additions (review §7 items 2/3/4/7): the 64-character DB cap
// (AI/Concept/03_00_Database_Static.md: username VARCHAR(64) … CHECK(length(username) <= 64)),
// read-only usernames for service accounts when editing (deliberate policy — the backend
// would permit renaming, see users.go:441), and the username error staying hidden until the
// form is interacted with.

import { CommonModule } from '@angular/common';
import { TestBed, ComponentFixture } from '@angular/core/testing';
import { ReactiveFormsModule, FormsModule } from '@angular/forms';
import { Router } from '@angular/router';
import { of, Subject } from 'rxjs';
import { AdminUserListComponent } from './admin-user-list.component';
import { ApiKeyTableComponent } from '../api-key-table/api-key-table.component';
import { AuthService } from '../../services/auth.service';
import { DatabaseService } from '../../services/database.service';
import { ModalService } from '../../services/modal.service';
import { NotificationService } from '../../services/notification.service';
import { ContentType, Database, User, ApiKey } from '../../models';

describe('AdminUserListComponent — FE-001 regression', () => {
  let fixture: ComponentFixture<AdminUserListComponent>;
  let component: AdminUserListComponent;
  let authSpy: jasmine.SpyObj<AuthService>;
  let databaseSpy: jasmine.SpyObj<DatabaseService>;
  let routerSpy: jasmine.SpyObj<Router>;

  const musicDb: Database = {
    id: 'db1',
    name: 'Music',
    content_type: ContentType.Audio,
    n_max_queued: 0,
    config: { create_preview: true, auto_conversion: '' },
    housekeeping: { interval: '1h', disk_space: '100G', max_age: '0' },
    custom_fields: [],
  };

  const adminUser: User = {
    id: 'admin-1',
    username: 'admin',
    is_admin: true,
    account_type: 'local',
    permissions: [],
  };

  const oidcUser: User = {
    id: 'u1',
    username: 'john.doe', // dotted OIDC username, as provisioned from preferred_username
    is_admin: false,
    account_type: 'oidc',
    permissions: [
      {
        database_id: 'db1',
        can_view: false,
        can_create: false,
        can_edit: false,
        can_delete: false,
        can_admin: false,
      },
    ],
  };

  const localUser: User = {
    id: 'u2',
    username: 'john.doe', // the backend imposes no character restriction on local names either
    is_admin: false,
    account_type: 'local',
    permissions: [
      {
        database_id: 'db1',
        can_view: false,
        can_create: false,
        can_edit: false,
        can_delete: false,
        can_admin: false,
      },
    ],
  };

  const serviceUser: User = {
    id: 'u3',
    username: 'ci-bot',
    is_admin: false,
    account_type: 'service_account',
    permissions: [
      {
        database_id: 'db1',
        can_view: false,
        can_create: false,
        can_edit: false,
        can_delete: false,
        can_admin: false,
      },
    ],
  };

  const allUsers: Record<string, User> = {
    u1: oidcUser,
    u2: localUser,
    u3: serviceUser,
    'admin-1': adminUser,
  };

  /** Text of the username error div (hidden until touched || submitted). */
  const usernameError = (): HTMLElement | undefined =>
    (Array.from(fixture.nativeElement.querySelectorAll('.form-error')) as HTMLElement[]).find(
      (el) => (el.textContent ?? '').includes('Username is required'),
    );

  beforeEach(async () => {
    authSpy = jasmine.createSpyObj<AuthService>('AuthService', [
      'getUsers',
      'getUser',
      'updateUser',
      'createUser',
      'deleteUser',
      'getUserKeys',
      'deleteUserKey',
      'getCurrentUser',
      'fetchCurrentUser',
      'logout',
    ]);
    databaseSpy = jasmine.createSpyObj<DatabaseService>('DatabaseService', ['loadDatabases']);
    routerSpy = jasmine.createSpyObj<Router>('Router', ['navigate']);
    const modalSpy = jasmine.createSpyObj<ModalService>('ModalService', ['open', 'close']);
    const notificationSpy = jasmine.createSpyObj<NotificationService>('NotificationService', [
      'showSuccess',
      'showError',
    ]);

    authSpy.getUsers.and.returnValue(of([oidcUser]));
    authSpy.getUser.and.callFake((id: string) => of(allUsers[id] ?? oidcUser));
    authSpy.getUserKeys.and.returnValue(of([]));
    authSpy.getCurrentUser.and.returnValue(adminUser);
    authSpy.fetchCurrentUser.and.returnValue(of(adminUser));
    authSpy.updateUser.and.callFake((_id: string, updates: Partial<User>) =>
      of({ ...oidcUser, ...updates } as User),
    );
    databaseSpy.loadDatabases.and.returnValue(of([musicDb]));

    await TestBed.configureTestingModule({
      declarations: [AdminUserListComponent],
      imports: [CommonModule, ReactiveFormsModule, FormsModule, ApiKeyTableComponent],
      providers: [
        { provide: AuthService, useValue: authSpy },
        { provide: DatabaseService, useValue: databaseSpy },
        { provide: ModalService, useValue: modalSpy },
        { provide: NotificationService, useValue: notificationSpy },
        { provide: Router, useValue: routerSpy },
      ],
    }).compileComponents();

    fixture = TestBed.createComponent(AdminUserListComponent);
    component = fixture.componentInstance;
    // Renders the template and runs ngOnInit once (users + databases load synchronously).
    fixture.detectChanges();
  });

  it('FE-001: saving a user named "john.doe" must call updateUser (dotted OIDC/local usernames were unsavable)', () => {
    // --- OIDC user (the reported scenario) ---
    component.selectUser(oidcUser);
    component.permissions.at(0)?.get('can_view')?.setValue(true);
    component.onSaveUser();

    expect(authSpy.updateUser)
      .withContext('updateUser must be called for an OIDC user named "john.doe"')
      .toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser.calls.mostRecent().args[1].username)
      .withContext('the username must survive into the PATCH payload')
      .toBe('john.doe');

    // --- same username on a local account: the validator must not reject it either ---
    authSpy.updateUser.calls.reset();
    component.selectUser(localUser);
    component.permissions.at(0)?.get('can_view')?.setValue(true);
    component.onSaveUser();

    expect(authSpy.updateUser)
      .withContext('updateUser must be called for a local user named "john.doe"')
      .toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser.calls.mostRecent().args[1].username).toBe('john.doe');
  });

  it('FE-001: the username control is read-only for an OIDC user but still reaches updateUser', () => {
    component.selectUser(oidcUser);
    fixture.detectChanges();

    expect(component.detailForm.get('username')?.disabled)
      .withContext('the IdP-owned username must be read-only when editing')
      .toBeTrue();
    expect(fixture.nativeElement.textContent)
      .withContext('the read-only reason must be shown')
      .toContain('Managed via Single Sign-On Identity Provider');

    component.permissions.at(0)?.get('can_view')?.setValue(true);
    component.onSaveUser();

    expect(authSpy.updateUser).toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser.calls.mostRecent().args[1].username)
      .withContext('getRawValue() must carry the disabled name into the PATCH payload')
      .toBe('john.doe');
  });

  it('FE-001: service-account usernames are read-only when editing and still reach updateUser', () => {
    component.selectUser(serviceUser);
    fixture.detectChanges();

    expect(component.detailForm.get('username')?.disabled)
      .withContext(
        'service-account names are fixed at creation (deliberate policy, review §7 item 3)',
      )
      .toBeTrue();
    expect(fixture.nativeElement.textContent)
      .withContext('the read-only reason must be shown for service accounts too')
      .toContain('Service account names are fixed at creation.');

    component.permissions.at(0)?.get('can_view')?.setValue(true);
    component.onSaveUser();

    expect(authSpy.updateUser).toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser.calls.mostRecent().args[1].username)
      .withContext('getRawValue() must still carry the disabled service-account name')
      .toBe('ci-bot');
  });

  it('FE-001: usernames are capped at 64 characters (DB CHECK(length(username) <= 64))', () => {
    component.selectUser(localUser);
    component.detailForm.get('username')?.setValue('a'.repeat(65));

    expect(component.detailForm.invalid)
      .withContext('a 65-character name dies at the DB constraint and must be rejected by the form')
      .toBeTrue();

    component.onSaveUser();
    fixture.detectChanges();
    expect(authSpy.updateUser).not.toHaveBeenCalled();
    expect(usernameError()?.textContent)
      .withContext('the error must name the real 64-character limit')
      .toContain('at most 64 characters');

    // Exactly 64 characters is valid and saves.
    component.detailForm.get('username')?.setValue('b'.repeat(64));
    component.onSaveUser();

    expect(authSpy.updateUser)
      .withContext('a 64-character name must save')
      .toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser.calls.mostRecent().args[1].username).toBe('b'.repeat(64));
  });

  it('FE-001: the username error stays hidden on the pristine create form and shows after submit', () => {
    component.createNewUser();
    fixture.detectChanges();

    expect(usernameError())
      .withContext('the create-user form must not show the required error before first interaction')
      .toBeUndefined();

    component.onSaveUser(); // empty name -> invalid -> submitted + markAllAsTouched
    fixture.detectChanges();

    expect(usernameError())
      .withContext('after a submit attempt the empty name must show the error')
      .toBeTruthy();
  });

  // --- FE-020: selectUser() response race -----------------------------------

  it('FE-020: an out-of-order response for the previously selected user must not overwrite the form', () => {
    // The full record for u1 resolves SLOWLY (simulated with a Subject), u2 fast.
    const slowUserOne$ = new Subject<User>();
    authSpy.getUser.and.callFake((id: string) =>
      id === 'u1' ? slowUserOne$.asObservable() : of(allUsers[id] ?? oidcUser),
    );

    component.selectUser(oidcUser); // request 1: in flight, does not resolve yet
    component.selectUser(localUser); // request 2: the user actually clicked last
    fixture.detectChanges();

    // The stale response for u1 arrives AFTER u2 was selected.
    slowUserOne$.next({ ...oidcUser, is_admin: true });
    slowUserOne$.complete();
    fixture.detectChanges();

    expect(component.selectedUser?.id)
      .withContext('the form must belong to the last-clicked user (u2)')
      .toBe('u2');
    expect(component.detailForm.get('id')?.value)
      .withContext('a slow response for the previously selected user must not repopulate the form')
      .toBe('u2');
  });

  it('FE-020: a failing detail fetch falls back to the clicked list record instead of killing the stream', () => {
    const failing$ = new Subject<User>();
    authSpy.getUser.and.callFake((id: string) =>
      id === 'u1' ? failing$.asObservable() : of(allUsers[id] ?? oidcUser),
    );

    component.selectUser(oidcUser);
    failing$.error(new Error('boom')); // fallback -> the shallow list record stays selected
    fixture.detectChanges();
    expect(component.selectedUser?.id).toBe('u1');

    // The selection stream must still be alive for the next click.
    component.selectUser(localUser);
    fixture.detectChanges();
    expect(component.detailForm.get('id')?.value).toBe('u2');
  });

  // --- FE-045: revoke-key race capture --------------------------------------

  it('FE-045: revoking a key targets the owner captured at open time, not the selection at confirm time', () => {
    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;
    const confirm$ = new Subject<boolean>();
    modalSpy.open.and.returnValue(confirm$.asObservable());
    authSpy.deleteUserKey.and.returnValue(of({ message: 'ok' }));

    component.selectUser(oidcUser); // u1 owns the key
    component.openRevokeKeyConfirm({ id: 'k1', name: 'backup' } as ApiKey);

    // The selection changes while the confirmation modal is open…
    component.selectUser(localUser);
    fixture.detectChanges();

    // …and only now does the admin confirm.
    confirm$.next(true);

    expect(authSpy.deleteUserKey)
      .withContext('the DELETE must go to the user that owned the key when the dialog opened')
      .toHaveBeenCalledWith('u1', 'k1');
  });

  // --- OIDC list badge (blue-box-with-no-text fix) --------------------------

  it('the SSO/OIDC list badge shows its "OIDC" text with the SA badge style (no opaque background)', () => {
    component.setListTab('oidc');
    fixture.detectChanges();

    const badges = Array.from(
      fixture.nativeElement.querySelectorAll('.service-badge'),
    ) as HTMLElement[];
    const oidcBadge = badges.find((b) => (b.textContent ?? '').trim() === 'OIDC');

    expect(oidcBadge)
      .withContext('the OIDC badge must render in the SSO/OIDC user list')
      .toBeTruthy();

    // The badge used to carry `style="background: var(--color-primary, #3b82f6)"` —
    // a solid blue box (the variable is undefined, so the #3b82f6 fallback applied)
    // behind --text-accent blue text, making "OIDC" invisible.
    expect(getComputedStyle(oidcBadge!).backgroundColor)
      .withContext('the badge must not paint an opaque background behind its blue text')
      .toBe('rgba(0, 0, 0, 0)');

    // Same style hook as the service-account "SA" badge (blue accent text).
    expect(oidcBadge!.classList.contains('service-badge')).toBeTrue();
    expect(oidcBadge!.style.background).toBe('');
  });

  it('exits draft mode after createUser so clicking Save twice creates once then updates', () => {
    const createdUser: User = {
      id: 'u-new',
      username: 'new_user',
      is_admin: false,
      account_type: 'local',
      permissions: [],
    };
    allUsers['u-new'] = createdUser;
    authSpy.getUsers.and.returnValue(of([oidcUser, createdUser]));
    authSpy.createUser.and.returnValue(of(createdUser));
    authSpy.updateUser.and.callFake((id: string, updates: Partial<User>) =>
      of({ ...createdUser, ...updates, id } as User),
    );

    component.createNewUser();
    component.detailForm.patchValue({
      username: 'new_user',
      password: 'password123',
    });
    component.onSaveUser();

    expect(authSpy.createUser).toHaveBeenCalledTimes(1);
    expect(component.isNewUser).toBeFalse();
    expect(component.selectedUser?.id).toBe('u-new');
    expect(component.detailForm.get('password')?.value).toBe('');

    component.detailForm.patchValue({ is_admin: true });
    component.onSaveUser();

    expect(authSpy.createUser).toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser).toHaveBeenCalledTimes(1);
    expect(authSpy.updateUser.calls.mostRecent().args[0]).toBe('u-new');
  });

  it('disables and guards against deleting the currently logged-in user', () => {
    const modalSpy = TestBed.inject(ModalService) as jasmine.SpyObj<ModalService>;
    component.selectUser(adminUser);
    fixture.detectChanges();

    expect(component.isSelectedCurrentUser()).toBeTrue();
    const deleteBtn = fixture.nativeElement.querySelector(
      '.detail-header .btn-danger',
    ) as HTMLButtonElement;
    expect(deleteBtn.disabled).toBeTrue();
    expect(deleteBtn.title).toContain('cannot delete your own account');

    component.openDeleteConfirm();
    expect(modalSpy.open).not.toHaveBeenCalled();
    expect(authSpy.deleteUser).not.toHaveBeenCalled();
  });

  it('refreshes current user and navigates to overview on self-demotion', () => {
    const notificationSpy = TestBed.inject(
      NotificationService,
    ) as jasmine.SpyObj<NotificationService>;
    authSpy.updateUser.and.callFake((id: string, updates: Partial<User>) =>
      of({ ...adminUser, ...updates, id } as User),
    );

    component.selectUser(adminUser);
    authSpy.getUsers.calls.reset();
    component.detailForm.patchValue({ is_admin: false });
    component.onSaveUser();

    expect(authSpy.updateUser).toHaveBeenCalledTimes(1);
    expect(authSpy.fetchCurrentUser).toHaveBeenCalledTimes(1);
    expect(routerSpy.navigate).toHaveBeenCalledWith(['/dashboard/overview']);
    expect(authSpy.getUsers).not.toHaveBeenCalled();

    // Self-password-change triggers sign-out
    component.detailForm.patchValue({ is_admin: true, password: 'newpassword123' });
    component.onSaveUser();
    expect(notificationSpy.showSuccess).toHaveBeenCalledWith(
      'User updated successfully. Signing out because your password changed.',
    );
    expect(authSpy.logout).toHaveBeenCalledWith(false);
  });

  it('clears stale keys immediately and updates API Keys tab count automatically when switching users', () => {
    const userOneKeys$ = new Subject<ApiKey[]>();
    const userTwoKeys$ = new Subject<ApiKey[]>();
    authSpy.getUserKeys.and.callFake((id: string) =>
      id === 'u1' ? userOneKeys$.asObservable() : userTwoKeys$.asObservable(),
    );

    const keySample = (id: string, name: string): ApiKey => ({
      id,
      name,
      key_hint: 'mh_abc',
      scope_view: true,
      scope_create: false,
      scope_edit: false,
      scope_delete: false,
      scope_admin: false,
      created_at: 1700000000000,
      expires_at: null,
      last_used_at: null,
    });

    component.selectUser(oidcUser);
    fixture.detectChanges();

    const keysTabText = () =>
      (Array.from(fixture.nativeElement.querySelectorAll('.detail-tab-btn')) as HTMLElement[])
        .find((el) => (el.textContent ?? '').includes('API Keys'))
        ?.textContent?.trim();

    expect(keysTabText()).toBe('API Keys (...)');

    userOneKeys$.next([keySample('k1', 'Key 1')]);
    userOneKeys$.complete();
    fixture.detectChanges();
    expect(keysTabText()).toBe('API Keys (1)');

    // Switching to another user immediately clears stale keys and shows loading indicator
    component.selectUser(localUser);
    fixture.detectChanges();
    expect(component.userKeys).toEqual([]);
    expect(keysTabText()).toBe('API Keys (...)');

    userTwoKeys$.next([keySample('k2', 'Key 2'), keySample('k3', 'Key 3')]);
    userTwoKeys$.complete();
    fixture.detectChanges();
    expect(keysTabText()).toBe('API Keys (2)');
  });

  it('omits permissions payload when saving an admin user', () => {
    component.selectUser(localUser);
    component.detailForm.patchValue({ is_admin: true });
    component.onSaveUser();

    expect(authSpy.updateUser).toHaveBeenCalledTimes(1);
    const payload = authSpy.updateUser.calls.mostRecent().args[1];
    expect(payload.is_admin).toBeTrue();
    expect('permissions' in payload).toBeFalse();
  });
});
