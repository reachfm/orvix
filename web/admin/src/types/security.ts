export interface FirewallRule {
  id: number;
  type: "allow" | "block";
  ip_range: string;
  description: string;
  created_at: string;
  created_by?: string;
}

export interface Session {
  id: string;
  user_email: string;
  ip_address: string;
  user_agent: string;
  created_at: string;
  last_active_at: string;
  is_current?: boolean;
}
