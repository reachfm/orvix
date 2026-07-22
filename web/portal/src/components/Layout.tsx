import { Link, useLocation, useNavigate } from 'react-router-dom'
import { cn, useAuthStore } from '@shared'

const navItems = [
  { path: '/', label: 'Dashboard', icon: '📊' },
  { path: '/domains', label: 'Domains', icon: '🌐' },
  { path: '/license', label: 'License', icon: '📄' },
  { path: '/billing', label: 'Billing', icon: '💰' },
  { path: '/support', label: 'Support', icon: '🎧' },
  { path: '/downloads', label: 'Downloads', icon: '⬇️' },
  { path: '/changelog', label: 'Changelog', icon: '📋' },
]

export function PortalLayout({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  const navigate = useNavigate()
  const { user, clearAuth } = useAuthStore()

  return (
    <div className="flex h-screen bg-bg-base">
      <aside className="w-64 border-r border-border bg-bg-surface flex flex-col">
        <div className="p-4 border-b border-border">
          <h1 className="text-xl font-bold text-accent">OrvixEM Portal</h1>
          <p className="text-xs text-text-muted mt-1">portal.orvix.email</p>
        </div>
        <nav className="flex-1 p-2 space-y-1">
          {navItems.map((item) => (
            <Link key={item.path} to={item.path}
              className={cn('flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors',
                location.pathname === item.path ? 'bg-accent text-white' : 'text-text-secondary hover:bg-bg-subtle hover:text-text-primary')}>
              <span className="text-base">{item.icon}</span>
              <span>{item.label}</span>
            </Link>
          ))}
        </nav>
        <div className="p-4 border-t border-border">
          <div className="flex items-center gap-3">
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-text-primary truncate">{user?.email}</p>
              <p className="text-xs text-text-muted">Customer Portal</p>
            </div>
            <button onClick={() => { clearAuth(); navigate('/login') }} className="text-text-secondary hover:text-text-primary" title="Logout">🚪</button>
          </div>
        </div>
      </aside>
      <main className="flex-1 overflow-y-auto p-6">{children}</main>
    </div>
  )
}
