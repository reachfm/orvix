export interface ApiKey {
  id: number;
  name: string;
  prefix: string;
  scopes: string[];
  created_at: string;
  last_used_at?: string;
  expires_at?: string;
  created_by?: string;
}

export interface CreateApiKeyResponse {
  api_key: ApiKey;
  secret: string;
}
