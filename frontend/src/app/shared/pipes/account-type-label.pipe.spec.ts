import { AccountTypeLabelPipe } from './account-type-label.pipe';

describe('AccountTypeLabelPipe', () => {
  let pipe: AccountTypeLabelPipe;

  beforeEach(() => {
    pipe = new AccountTypeLabelPipe();
  });

  it('formats account types in default full style', () => {
    expect(pipe.transform('oidc')).toBe('Single Sign-On (OIDC)');
    expect(pipe.transform('service_account')).toBe('Service Account');
    expect(pipe.transform('local')).toBe('Local User');
    expect(pipe.transform(null)).toBe('Local User');
    expect(pipe.transform(undefined)).toBe('Local User');
  });

  it('formats account types in short style', () => {
    expect(pipe.transform('oidc', 'short')).toBe('SSO User');
    expect(pipe.transform('service_account', 'short')).toBe('Service Account');
    expect(pipe.transform('local', 'short')).toBe('Standard User');
    expect(pipe.transform(null, 'short')).toBe('Standard User');
    expect(pipe.transform(undefined, 'short')).toBe('Standard User');
  });

  it('formats account types in action style', () => {
    expect(pipe.transform('oidc', 'action')).toBe('OIDC User');
    expect(pipe.transform('service_account', 'action')).toBe('Service Account');
    expect(pipe.transform('local', 'action')).toBe('User');
    expect(pipe.transform(null, 'action')).toBe('User');
    expect(pipe.transform(undefined, 'action')).toBe('User');
  });

  it('ignores Object.prototype keys such as constructor and toString', () => {
    expect(pipe.transform('constructor')).toBe('Local User');
    expect(pipe.transform('toString', 'short')).toBe('Standard User');
    expect(pipe.transform('valueOf', 'action')).toBe('User');
  });
});
