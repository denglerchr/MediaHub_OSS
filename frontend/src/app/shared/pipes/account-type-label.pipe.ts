import { Pipe, PipeTransform } from '@angular/core';

export type AccountTypeLabelStyle = 'full' | 'short' | 'action';

const ACCOUNT_TYPE_LABELS: Record<AccountTypeLabelStyle, Record<string, string>> = {
  full: {
    oidc: 'Single Sign-On (OIDC)',
    service_account: 'Service Account',
  },
  short: {
    oidc: 'SSO User',
    service_account: 'Service Account',
  },
  action: {
    oidc: 'OIDC User',
    service_account: 'Service Account',
  },
};

const DEFAULT_LABELS: Record<AccountTypeLabelStyle, string> = {
  full: 'Local User',
  short: 'Standard User',
  action: 'User',
};

/**
 * Formats a user's `account_type` into a human-readable label in 'full', 'short', or 'action' style.
 */
@Pipe({
  name: 'accountTypeLabel',
  standalone: true,
})
export class AccountTypeLabelPipe implements PipeTransform {
  transform(accountType: string | null | undefined, style: AccountTypeLabelStyle = 'full'): string {
    const table = Object.prototype.hasOwnProperty.call(ACCOUNT_TYPE_LABELS, style)
      ? ACCOUNT_TYPE_LABELS[style]
      : ACCOUNT_TYPE_LABELS.full;
    if (accountType && Object.prototype.hasOwnProperty.call(table, accountType)) {
      return table[accountType];
    }
    return Object.prototype.hasOwnProperty.call(DEFAULT_LABELS, style)
      ? DEFAULT_LABELS[style]
      : DEFAULT_LABELS.full;
  }
}
