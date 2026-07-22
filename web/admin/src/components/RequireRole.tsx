import { Navigate } from 'react-router-dom'
import { useAuthStore, type UserRole } from '@shared'

interface RequireRoleProps {
  role: UserRole | UserRole[]
  children: React.ReactNode
  fallback?: React.ReactNode
}

export function RequireRole({ role, children, fallback }: RequireRoleProps) {
  const getUserRole = useAuthStore((s) => s.getUserRole)
  const token = useAuthStore((s) => s.token)
  const currentRole = getUserRole()

  if (!token) return <Navigate to="/login" replace />

  const allowed = Array.isArray(role) ? role : [role]
  if (!currentRole || !allowed.includes(currentRole)) {
    if (fallback) return <>{fallback}</>
    return (
      <div className="flex items-center justify-center h-full text-center p-8">
        <div>
          <h2 className="text-2xl font-bold text-danger mb-2">403 Forbidden</h2>
          <p className="text-text-secondary">
            You do not have permission to access this page.
            Required role: <strong>{Array.isArray(role) ? role.join(' | ') : role}</strong>.
            Your role: <strong>{currentRole || 'none'}</strong>.
          </p>
        </div>
      </div>
    )
  }

  return <>{children}</>
}
