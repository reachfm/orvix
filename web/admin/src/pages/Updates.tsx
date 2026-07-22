import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Button, Badge, Loading } from '@shared'

export function UpdatesPage() {
  const { data: version } = useQuery({ queryKey: ['version'], queryFn: () => apiRequest<any>('/version') })

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Updates</h1>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
        <Card title="Current Version">
          <div className="space-y-3 mt-2">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Version</span><span className="font-bold text-text-primary">{version?.version || '0.1.0'}</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Product</span><span className="text-text-primary">{version?.product || 'OrvixEM'}</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Channel</span><Badge>{version?.channel || 'nightly'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Commit</span><span className="font-mono text-xs text-text-secondary">{version?.commit || '-'}</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Build Date</span><span className="text-text-secondary">{version?.build_date || '-'}</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Go Version</span><span className="text-text-secondary">{version?.go_version || '1.23'}</span></div>
          </div>
        </Card>

        <Card title="Update Channels">
          <div className="space-y-3 mt-2">
            <div className="flex items-center justify-between p-2 rounded bg-bg-elevated">
              <div><p className="text-sm font-medium text-text-primary">Stable</p><p className="text-xs text-text-secondary">Production-safe releases</p></div>
              <Badge variant="success">Current</Badge>
            </div>
            <div className="flex items-center justify-between p-2 rounded bg-bg-elevated">
              <div><p className="text-sm font-medium text-text-primary">Beta</p><p className="text-xs text-text-secondary">Feature validation</p></div>
              <Badge>Available</Badge>
            </div>
            <div className="flex items-center justify-between p-2 rounded bg-bg-elevated">
              <div><p className="text-sm font-medium text-text-primary">Early Access</p><p className="text-xs text-text-secondary">Partner preview</p></div>
              <Badge>Available</Badge>
            </div>
            <div className="flex items-center justify-between p-2 rounded bg-bg-elevated">
              <div><p className="text-sm font-medium text-text-primary">Nightly</p><p className="text-xs text-text-secondary">Internal builds</p></div>
              <Badge variant="warning">Current</Badge>
            </div>
          </div>
        </Card>
      </div>

      <Card title="Actions" className="mb-6">
        <div className="flex gap-3">
          <Button>Check for Updates</Button>
          <Button variant="secondary">View Changelog</Button>
          <Button variant="secondary">Rollback</Button>
        </div>
        <p className="text-text-secondary text-sm mt-3">Updates are downloaded from <strong>updates.orvix.email</strong> with signature verification and automatic rollback on failure.</p>
      </Card>
    </div>
  )
}
