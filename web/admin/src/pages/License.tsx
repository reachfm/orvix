import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Button, Loading, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, EmptyState } from '@shared'

export function LicensePage() {
  const { data, isLoading } = useQuery({
    queryKey: ['license'],
    queryFn: () => apiRequest<any>('/license/status'),
  })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">License</h1>
      
      {!data?.active ? (
        <Card>
          <div className="text-center py-8">
            <div className="text-4xl mb-4">🔑</div>
        <h2 className="text-xl font-semibold text-text-primary mb-2">No Active License</h2>
        <p className="text-text-secondary mb-4">Contact sales at <strong>licensing@orvix.email</strong> to purchase a license key.</p>
        <div className="flex justify-center gap-3 mb-6">
          <a href="https://orvix.email/pricing" target="_blank" rel="noopener noreferrer" className="inline-flex items-center rounded-lg bg-accent px-6 py-2 text-sm font-medium text-white hover:bg-accent-hover">Purchase License</a>
          <a href="https://license.orvix.email" target="_blank" rel="noopener noreferrer" className="inline-flex items-center rounded-lg bg-bg-elevated border border-border px-6 py-2 text-sm font-medium text-text-primary hover:bg-bg-subtle">Activate License</a>
        </div>
            <div className="bg-bg-elevated rounded-lg p-4 inline-block text-left">
              <p className="text-sm text-text-secondary">Pricing:</p>
              <ul className="text-sm text-text-primary mt-2 space-y-1">
                <li>SMB — $500/year (10 domains, 500 mailboxes)</li>
                <li>ISP — $1,200/year (unlimited domains, 50k mailboxes)</li>
                <li>Enterprise — $2,500/year (unlimited everything)</li>
              </ul>
            </div>
          </div>
        </Card>
      ) : (
        <div className="space-y-6">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <Card><p className="text-text-secondary text-sm">Tier</p><p className="text-2xl font-bold text-text-primary mt-1 capitalize">{data.tier}</p></Card>
            <Card><p className="text-text-secondary text-sm">Expires</p><p className="text-2xl font-bold text-text-primary mt-1">{new Date(data.expires_at).toLocaleDateString()}</p></Card>
            <Card><p className="text-text-secondary text-sm">Hardware Binding</p><p className="text-2xl font-bold text-text-primary mt-1">{data.hardware_binding ? 'Yes' : 'No'}</p></Card>
          </div>
          <Card title="Usage">
            <div className="space-y-4">
              <div><div className="flex justify-between text-sm mb-1"><span className="text-text-secondary">Domains ({data.used_domains} / {data.max_domains})</span><span className="text-text-primary">{Math.round((data.used_domains / data.max_domains) * 100)}%</span></div><div className="h-2 bg-bg-elevated rounded-full"><div className="h-full bg-accent rounded-full" style={{ width: `${(data.used_domains / data.max_domains) * 100}%` }} /></div></div>
              <div><div className="flex justify-between text-sm mb-1"><span className="text-text-secondary">Mailboxes ({data.used_mailboxes} / {data.max_mailboxes})</span><span className="text-text-primary">{Math.round((data.used_mailboxes / data.max_mailboxes) * 100)}%</span></div><div className="h-2 bg-bg-elevated rounded-full"><div className="h-full bg-success rounded-full" style={{ width: `${(data.used_mailboxes / data.max_mailboxes) * 100}%` }} /></div></div>
            </div>
          </Card>
        </div>
      )}
    </div>
  )
}
