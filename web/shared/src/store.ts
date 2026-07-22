import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type UserRole = 'super_admin' | 'tenant_admin' | 'admin' | 'user' | null

interface UserInfo {
  id: number
  email: string
  role: string
  tenant_id?: number | null
}

interface AuthState {
  token: string | null
  refreshToken: string | null
  user: UserInfo | null
  setAuth: (token: string, refreshToken: string, user: UserInfo) => void
  clearAuth: () => void
  getAccessToken: () => string | null
  /** Returns the parsed role for RBAC decisions */
  getUserRole: () => UserRole
  /** Returns the tenant_id for tenant-scoped operations */
  getTenantId: () => number | null
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
      token: null,
      refreshToken: null,
      user: null,
      setAuth: (token, refreshToken, user) => set({ token, refreshToken, user }),
      clearAuth: () => set({ token: null, refreshToken: null, user: null }),
      getAccessToken: () => get().token,
      getUserRole: () => {
        const role = get().user?.role
        if (role === 'super_admin' || role === 'tenant_admin') return role as UserRole
        if (role === 'admin') return 'admin'
        if (role === 'user') return 'user'
        return null
      },
      getTenantId: () => get().user?.tenant_id ?? null,
    }),
    {
      name: 'orvixem-auth',
      partialize: (state) => ({
        refreshToken: state.refreshToken,
        user: state.user,
      }),
    }
  )
)
