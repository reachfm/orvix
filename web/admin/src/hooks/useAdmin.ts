import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import {
  createTenant,
  fetchTenants,
  createTenantUser,
  fetchTenantUsers,
  type CreateTenantPayload,
  type CreateUserPayload,
} from '@shared/api/admin'

// ---------- Super Admin Hooks ----------

export function useTenants() {
  return useQuery({
    queryKey: ['admin', 'tenants'],
    queryFn: fetchTenants,
  })
}

export function useCreateTenant() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload: CreateTenantPayload) => createTenant(payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'tenants'] })
    },
  })
}

// ---------- Tenant Admin Hooks ----------

export function useTenantUsers() {
  return useQuery({
    queryKey: ['admin', 'tenant-users'],
    queryFn: fetchTenantUsers,
  })
}

export function useCreateTenantUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (payload: CreateUserPayload) => createTenantUser(payload),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['admin', 'tenant-users'] })
    },
  })
}
