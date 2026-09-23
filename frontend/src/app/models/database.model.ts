import { ContentType } from './enums';

export type CustomFieldType = 'TEXT' | 'INTEGER' | 'REAL' | 'BOOLEAN' | 'COORDINATE';

export interface CustomField {
  id?: number;
  name: string;
  type: CustomFieldType;
  is_indexed?: boolean;
}

export interface Housekeeping {
  interval: string;
  disk_space: string;
  max_age: string;
}

export interface Stats {
  entry_count: number;
  total_disk_space_bytes: number;
}

export interface DatabaseConfig {
  create_preview?: boolean;
  auto_conversion?: string; 
}

export interface Database {
  id: string; // NEW: Added the ULID property
  name: string;
  content_type: ContentType; 
  n_max_queued: number;
  config: DatabaseConfig;
  housekeeping: Housekeeping;
  custom_fields: CustomField[];
  stats?: Stats;
}

export interface HousekeepingReport {
  database_id: string; // NEW: Added ULID reference
  database_name: string;
  entries_deleted: number;
  space_freed_bytes: number;
  message: string;
}