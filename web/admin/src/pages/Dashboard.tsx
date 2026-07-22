import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Loading } from '@shared'

export function Dashboard() {
  const { data: stats, isLoading: statsLoading } = useQuery({
    queryKey: ['admin-stats'],
    queryFn: () => apiRequest<any>('/admin/stats'),
  })

  const { data: license } = useQuery({
    queryKey: ['license'],
    queryFn: () => apiRequest<any>('/license/status'),
  })

  const { data: health } = useQuery({
    queryKey: ['health'],
    queryFn: () => apiRequest<any>('/health'),
  })

  if (statsLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Dashboard</h1>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4 mb-6">
        <Card>
          <p className="text-text-secondary text-sm">Domains</p>
          <p className="text-3xl font-bold text-text-primary mt-1">{stats?.domains || 0}</p>
        </Card>
        <Card>
          <p className="text-text-secondary text-sm">Users</p>
          <p className="text-3xl font-bold text-text-primary mt-1">{stats?.users || 0}</p>
        </Card>
        <Card>
          <p className="text-text-secondary text-sm">Product</p>
          <p className="text-xl font-bold text-accent mt-1">{stats?.product || 'OrvixEM'}</p>
        </Card>
        <Card>
          <p className="text-text-secondary text-sm">Version</p>
          <p className="text-xl font-bold text-text-primary mt-1">{stats?.version || '0.1.0'}</p>
        </Card>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Card title="System Health">
          {health ? (
            <div className="space-y-3">
              <div className="flex justify-between items-center">
                <span className="text-text-secondary">Database</span>
                <span className={health.database?.ready ? 'text-success' : 'text-danger'}>
                  {health.database?.ready ? '● Ready' : '● Error'}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-text-secondary">Stalwart</span>
                <span className={health.stalwart?.running ? 'text-success' : 'text-warning'}>
                  {health.stalwart?.running ? '● Running' : '● Not Running'}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-text-secondary">Status</span>
                <span className="text-success capitalize">● {health.status}</span>
              </div>
            </div>
          ) : (
            <p className="text-text-muted">Loading health data...</p>
          )}
        </Card>

        <Card title="License">
          {license ? (
            <div className="space-y-3">
              <div className="flex justify-between items-center">
                <span className="text-text-secondary">Active</span>
                <span className={license.active ? 'text-success' : 'text-warning'}>
                  {license.active ? '● Yes' : '● No'}
                </span>
              </div>
              <div className="flex justify-between items-center">
                <span className="text-text-secondary">Tier</span>
                <span className="text-text-primary capitalize">{license.tier || 'unknown'}</span>
              </div>
              {license.active && (
                <>
                  <div className="flex justify-between items-center">
                    <span className="text-text-secondary">Domains Used</span>
                    <span className="text-text-primary">{license.used_domains} / {license.max_domains}</span>
                  </div>
                  <div className="flex justify-between items-center">
                    <span className="text-text-secondary">Mailboxes Used</span>
                    <span className="text-text-primary">{license.used_mailboxes} / {license.max_mailboxes}</span>
                  </div>
                </>
              )}
            </div>
          ) : (
            <p className="text-text-muted">Loading license data...</p>
          )}
        </Card>
      </div>
    </div>
  )
}
