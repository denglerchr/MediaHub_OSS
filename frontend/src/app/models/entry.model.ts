import { EntryStatus } from './enums'; 

export interface MediaFields {
  width?: number;
  height?: number;
  duration?: number;
  channels?: number;
}

export interface Coordinate {
  latitude: number;
  longitude: number;
}

export interface CoordinateBoundingBox {
  min_lat: number;
  max_lat: number;
  min_lng: number;
  max_lng: number;
}

export interface Entry {
  id: number;
  timestamp: number;
  created_at: number;
  updated_at: number;
  status: EntryStatus | string; 
  database_id?: string; 
  
  // These might be undefined if the entry is still 'processing' (Async upload)
  mime_type?: string;
  filesize?: number;
  preview_filesize?: number; 
  filename?: string;

  // Grouped metadata arrays (Used consistently across all GET endpoints now)
  media_fields?: MediaFields;
  custom_fields?: Record<string, any>;
}

export interface PartialEntryResponse {
  id: number;
  timestamp: number;
  created_at: number;
  updated_at: number;
  database_id: string;
  status: EntryStatus | string;
  custom_fields?: Record<string, any>; 
}

export interface SearchFilter {
  operator: string;
  conditions?: SearchFilter[];
  field?: string;
  value?: any;
}

export interface SearchSort {
  field: string;
  direction: 'asc' | 'desc';
}

export interface SearchPagination {
  offset?: number;
  limit: number;
}

export interface SearchRequest {
  filter?: SearchFilter;
  sort?: SearchSort;
  pagination: SearchPagination;
}