// Define the Permission structure based on the new concept
export interface Permission {
  database_id: string; // UPDATED: Replaced database_name with database_id
  can_view: boolean;
  can_create: boolean;
  can_edit: boolean;
  can_delete: boolean;
  can_admin: boolean;
}

export type AccountType = 'local' | 'service_account' | 'oidc';

// Update the User interface to perfectly match the backend JSON
export interface User {
  id: string; // ULID
  username: string;
  is_admin: boolean;
  account_type: AccountType;
  permissions: Permission[]; 
  
  // Optional tracking fields (if your backend still returns them)
  created_at?: string;
  updated_at?: string;
}