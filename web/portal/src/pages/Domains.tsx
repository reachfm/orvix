import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Loading, EmptyState } from '@shared'

export function DomainsPage() {
  const { data, isLoading } = useQuery({ queryKey: ['portal-domains'], queryFn: () => apiRequest<any>('/admin/domains') })
  if (isLoading) return <Loading className="h-64" />
  const domains = data?.domains || []

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">My Domains</h1>
      {domains.length === 0 ? (
        <EmptyState title="No domains" description="Domains managed under your account will appear here." />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Domain</TableHeader><TableHeader>Status</TableHeader><TableHeader>DKIM Selector</TableHeader><TableHeader>DMARC</TableHeader><TableHeader>Created</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {domains.map((d: any) => (
              <TableRow key={d.id}>
                <TableCell className="font-medium">{d.name}</TableCell>
                <TableCell><Badge variant={d.status === 'active' ? 'success' : 'warning'}>{d.status}</Badge></TableCell>
                <TableCell className="font-mono text-xs">{d.dkim_selector || '-'}</TableCell>
                <TableCell className="text-text-secondary">{d.dmarc_policy || '-'}</TableCell>
                <TableCell className="text-text-secondary">{new Date(d.created_at).toLocaleDateString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
