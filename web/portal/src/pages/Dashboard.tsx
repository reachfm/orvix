import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Badge, Loading } from '@shared'

export function DashboardPage() {
  const { data: stats, isLoading } = useQuery({ queryKey: ['portal-stats'], queryFn: () => apiRequest<any>('/admin/stats') })
  const { data: license } = useQuery({ queryKey: ['portal-license'], queryFn: () => apiRequest<any>('/license/status') })
  const { data: health } = useQuery({ queryKey: ['portal-health'], queryFn: () => apiRequest<any>('/health') })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Dashboard</h1>
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
        <Card><p className="text-text-secondary text-sm">Product</p><p className="text-2xl font-bold text-accent mt-1">{stats?.product || 'OrvixEM'}</p></Card>
        <Card><p className="text-text-secondary text-sm">Version</p><p className="text-2xl font-bold text-text-primary mt-1">{stats?.version || '0.1.0'}</p></Card>
        <Card><p className="text-text-secondary text-sm">Server Status</p><p className="text-2xl font-bold text-success mt-1">● Online</p></Card>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <Card title="License">
          {license ? (
            <div className="space-y-3">
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Tier</span><Badge variant={license.tier === 'enterprise' ? 'info' : license.tier === 'isp' ? 'warning' : 'default'}>{license.tier || 'unknown'}</Badge></div>
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Domains</span><span className="text-text-primary">{license.used_domains || 0} / {license.max_domains || '-'}</span></div>
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Mailboxes</span><span className="text-text-primary">{license.used_mailboxes || 0} / {license.max_mailboxes || '-'}</span></div>
            </div>
          ) : <p className="text-text-muted">No active license</p>}
        </Card>

        <Card title="Quick Links">
          <div className="space-y-2">
            <a href="https://docs.orvix.email" target="_blank" rel="noopener noreferrer" className="block p-3 rounded bg-bg-elevated text-sm text-accent hover:underline">📚 Documentation</a>
            <a href="https://status.orvix.email" target="_blank" rel="noopener noreferrer" className="block p-3 rounded bg-bg-elevated text-sm text-accent hover:underline">📊 Service Status</a>
            <a href="https://mail.orvix.email" target="_blank" rel="noopener noreferrer" className="block p-3 rounded bg-bg-elevated text-sm text-accent hover:underline">✉️ Webmail</a>
          </div>
        </Card>
      </div>
    </div>
  )
}
