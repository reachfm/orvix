import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Badge, Loading } from '@shared'

export function LicensePage() {
  const { data, isLoading } = useQuery({ queryKey: ['portal-license'], queryFn: () => apiRequest<any>('/license/status') })
  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">License Overview</h1>
      {!data?.active ? (
        <Card>
          <div className="text-center py-8">
            <div className="text-4xl mb-4">🔑</div>
            <h2 className="text-xl font-semibold text-text-primary mb-2">No Active License</h2>
            <p className="text-text-secondary">Contact sales at <strong>licensing@orvix.email</strong> to purchase a license.</p>
            <div className="mt-4 flex justify-center gap-3">
              <div className="bg-bg-elevated rounded-lg p-4 text-center">
                <p className="text-lg font-bold text-text-primary">SMB</p>
                <p className="text-text-secondary text-sm">$500/yr</p>
              </div>
              <div className="bg-bg-elevated rounded-lg p-4 text-center">
                <p className="text-lg font-bold text-text-primary">ISP</p>
                <p className="text-text-secondary text-sm">$1,200/yr</p>
              </div>
              <div className="bg-bg-elevated rounded-lg p-4 text-center">
                <p className="text-lg font-bold text-text-primary">Enterprise</p>
                <p className="text-text-secondary text-sm">$2,500/yr</p>
              </div>
            </div>
          </div>
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Card title="Plan Details">
            <div className="space-y-3">
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Tier</span><Badge variant={data.tier === 'enterprise' ? 'info' : data.tier === 'isp' ? 'warning' : 'default'}>{data.tier}</Badge></div>
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Max Domains</span><span className="text-text-primary">{data.max_domains}</span></div>
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Max Mailboxes</span><span className="text-text-primary">{data.max_mailboxes}</span></div>
              <div className="flex justify-between text-sm"><span className="text-text-secondary">Expires</span><span className="text-text-primary">{new Date(data.expires_at).toLocaleDateString()}</span></div>
            </div>
          </Card>
          <Card title="Usage">
            <div className="space-y-4 mt-2">
              <div>
                <div className="flex justify-between text-sm mb-1"><span className="text-text-secondary">Domains ({data.used_domains} / {data.max_domains})</span></div>
                <div className="h-2 bg-bg-elevated rounded-full"><div className="h-full bg-accent rounded-full" style={{ width: `${(data.used_domains / data.max_domains) * 100}%` }} /></div>
              </div>
              <div>
                <div className="flex justify-between text-sm mb-1"><span className="text-text-secondary">Mailboxes ({data.used_mailboxes} / {data.max_mailboxes})</span></div>
                <div className="h-2 bg-bg-elevated rounded-full"><div className="h-full bg-success rounded-full" style={{ width: `${(data.used_mailboxes / data.max_mailboxes) * 100}%` }} /></div>
              </div>
            </div>
          </Card>
        </div>
      )}
    </div>
  )
}
