export interface User {
  id: number;
  email: string;
  name?: string;
  role: "superadmin" | "platform_super_admin" | "admin" | "user";
  status: "active" | "suspended";
  tenant_id?: number;
  tenant_name?: string;
  created_at: string;
  updated_at: string;
  last_login?: string;
}

export interface ListUsersResponse {
  users: User[];
  total: number;
  page: number;
  page_size: number;
}
