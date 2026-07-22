import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Loading } from '@shared'

export function FeaturesPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['features'],
    queryFn: () => apiRequest<any>('/features'),
  })

  if (isLoading) return <Loading className="h-64" />

  const features = data?.features || []

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Feature Flags</h1>
      <Card>
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Key</TableHeader>
              <TableHeader>Name</TableHeader>
              <TableHeader>Tier</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Source</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {features.map((f: any) => (
              <TableRow key={f.id}>
                <TableCell className="font-mono text-xs">{f.key}</TableCell>
                <TableCell className="font-medium">{f.name}</TableCell>
                <TableCell><Badge variant={f.tier === 'enterprise' ? 'info' : f.tier === 'isp' ? 'warning' : 'default'}>{f.tier}</Badge></TableCell>
                <TableCell><Badge variant={f.enabled ? 'success' : 'danger'}>{f.enabled ? 'Enabled' : 'Disabled'}</Badge></TableCell>
                <TableCell className="text-text-secondary">{f.source}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>
    </div>
  )
}
