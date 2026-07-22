import { Link, useLocation, useNavigate } from 'react-router-dom'
import { useAuthStore, cn } from '@shared'
import type { UserRole } from '@shared'

interface NavItem {
  path: string
  label: string
  icon: string
  roles: UserRole[]
}

const allNavItems: NavItem[] = [
  // Super Admin items
  { path: '/', label: 'Platform Dashboard', icon: '📊', roles: ['super_admin', 'tenant_admin', 'admin'] },
  { path: '/tenants', label: 'Tenants Management', icon: '🏢', roles: ['super_admin'] },
  { path: '/users', label: 'Users & Mailboxes', icon: '👥', roles: ['tenant_admin', 'admin'] },
  { path: '/domains', label: 'Domains', icon: '🌐', roles: ['tenant_admin', 'admin'] },

  // Shared operational items
  { path: '/mail-queue', label: 'Mail Queue', icon: '📬', roles: ['super_admin', 'tenant_admin'] },
  { path: '/firewall', label: 'Firewall', icon: '🛡️', roles: ['super_admin', 'tenant_admin'] },
  { path: '/geo-blocking', label: 'Geo-Blocking', icon: '🗺️', roles: ['super_admin', 'tenant_admin'] },
  { path: '/anti-spam', label: 'Anti-Spam', icon: '🚫', roles: ['super_admin', 'tenant_admin'] },
  { path: '/guardian', label: 'Guardian AI', icon: '🤖', roles: ['super_admin', 'tenant_admin'] },
  { path: '/auto-heal', label: 'Auto-Heal', icon: '🔧', roles: ['super_admin', 'tenant_admin'] },
  { path: '/dns', label: 'DNS Wizard', icon: '🔍', roles: ['super_admin', 'tenant_admin'] },
  { path: '/backup', label: 'Backup', icon: '💾', roles: ['super_admin', 'tenant_admin'] },
  { path: '/audit-logs', label: 'Audit Logs', icon: '📝', roles: ['super_admin', 'tenant_admin'] },
  { path: '/webhooks', label: 'Webhooks', icon: '🪝', roles: ['super_admin', 'tenant_admin'] },
  { path: '/api-keys', label: 'API Keys', icon: '🔐', roles: ['super_admin', 'tenant_admin'] },
  { path: '/logs', label: 'Log Viewer', icon: '📋', roles: ['super_admin', 'tenant_admin'] },
  { path: '/updates', label: 'Updates', icon: '⬆️', roles: ['super_admin', 'tenant_admin'] },
  { path: '/maintenance', label: 'Maintenance', icon: '🔧', roles: ['super_admin'] },

  // License & system (read-only for most)
  { path: '/license', label: 'License', icon: '📄', roles: ['super_admin', 'tenant_admin'] },
  { path: '/features', label: 'Feature Flags', icon: '🚩', roles: ['super_admin'] },
  { path: '/settings', label: 'Global Settings', icon: '⚙️', roles: ['super_admin'] },
]

export function Layout({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  const navigate = useNavigate()
  const { user, clearAuth, getUserRole } = useAuthStore()
  const role = getUserRole()

  const filteredItems = allNavItems.filter((item) => item.roles.includes(role))

  const handleLogout = () => {
    clearAuth()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-bg-base">
      <aside className="w-64 border-r border-border bg-bg-surface flex flex-col">
        <div className="p-4 border-b border-border">
          <h1 className="text-xl font-bold text-accent">OrvixEM Admin</h1>
          <p className="text-xs text-text-muted mt-1 capitalize">{role?.replace('_', ' ')}</p>
        </div>

        <nav className="flex-1 overflow-y-auto p-2 space-y-1">
          {filteredItems.map((item) => (
            <Link
              key={item.path}
              to={item.path}
              className={cn(
                'flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors',
                location.pathname === item.path
                  ? 'bg-accent text-white'
                  : 'text-text-secondary hover:bg-bg-subtle hover:text-text-primary'
              )}
            >
              <span className="text-base">{item.icon}</span>
              <span>{item.label}</span>
            </Link>
          ))}
        </nav>

        <div className="p-4 border-t border-border">
          <div className="flex items-center gap-3">
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-text-primary truncate">{user?.email}</p>
              <p className="text-xs text-text-muted capitalize">{user?.role}</p>
            </div>
            <button onClick={handleLogout} className="text-text-secondary hover:text-text-primary" title="Logout">
              🚪
            </button>
          </div>
        </div>
      </aside>

      <main className="flex-1 overflow-y-auto p-6">{children}</main>
    </div>
  )
}
