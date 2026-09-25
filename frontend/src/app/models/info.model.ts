export interface AppInfo {
  service_name: string;
  version: string;
  uptime: string; 
  conversion_to?: {
    image?: string[];
    audio?: string[];
    video?: string[];
  };
  oidc?: {
    enabled: boolean;
    login_page_disabled: boolean;
    oidc_issuer_url?: string;
    oidc_client_id?: string;
    oidc_redirect_url?: string;
    oidc_auth_endpoint?: string;
  };
  features?: {
    audit_logs: boolean;
  };
}