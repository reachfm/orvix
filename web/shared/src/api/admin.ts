import { apiRequest } from '../api'

// ---------- Type definitions ----------

export interface CreateTenantPayload {
  name: string
  plan: 'trial' | 'starter' | 'pro' | 'enterprise'
  email: string
  password: string
}

export interface Tenant {
  id: number
  name: string
  slug: string
  tier: string
  max_domains: number
  max_mailboxes: number
  active: boolean
  created_at: string
  updated_at: string
}

export interface CreateTenantResponse {
  tenant: Tenant
  admin: {
    id: number
    email: string
    role: string
  }
}

export interface CreateUserPayload {
  email: string
  password: string
  name: string
}

export interface User {
  id: number
  email: string
  username: string
  role: string
  is_active: boolean
  tenant_id: number
  created_at: string
}

export interface TenantUsersResponse {
  users: User[]
  tenant_id: number
}

// ---------- Super Admin API ----------

export function createTenant(payload: CreateTenantPayload): Promise<CreateTenantResponse> {
  return apiRequest<CreateTenantResponse>('/admin/v1/super/tenants', {
    method: 'POST',
    body: payload,
  })
}

export function fetchTenants(): Promise<{ tenants: Tenant[] }> {
  return apiRequest('/admin/v1/super/tenants')
}

// ---------- Tenant Admin API ----------

export function createTenantUser(payload: CreateUserPayload): Promise<User> {
  return apiRequest<User>('/admin/v1/tenant/users', {
    method: 'POST',
    body: payload,
  })
}

export function fetchTenantUsers(): Promise<TenantUsersResponse> {
  return apiRequest<TenantUsersResponse>('/admin/v1/tenant/users')
}
