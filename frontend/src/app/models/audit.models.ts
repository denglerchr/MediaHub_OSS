export interface AuditLog {
  id: number;
  timestamp: number;
  action: string;
  actor: string;
  resource: string;
  details: Record<string, any>;
}
