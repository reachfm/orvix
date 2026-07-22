import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, EmptyState, Loading } from '@shared'

export function ProvisioningJobsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['provisioning-jobs'],
    queryFn: () => apiRequest<any>('/admin/provisioning-jobs'),
  })

  if (isLoading) return <Loading className="h-64" />

  const jobs = data?.provisioning_jobs || []

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Provisioning Jobs</h1>

      {jobs.length === 0 ? (
        <EmptyState title="No provisioning jobs" description="Jobs appear when domains are created through the admin panel." />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Domain</TableHeader>
              <TableHeader>Type</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Stalwart</TableHeader>
              <TableHeader>DNS</TableHeader>
              <TableHeader>Error</TableHeader>
              <TableHeader>Completed</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {jobs.map((j: any) => (
              <TableRow key={j.id}>
                <TableCell>{j.id}</TableCell>
                <TableCell className="font-medium">{j.domain_name}</TableCell>
                <TableCell><Badge>{j.type}</Badge></TableCell>
                <TableCell><Badge variant={j.status === 'completed' ? 'success' : j.status === 'running' ? 'info' : 'warning'}>{j.status}</Badge></TableCell>
                <TableCell><Badge variant={j.stalwart_result === 'ok' ? 'success' : j.stalwart_result === 'failed' ? 'danger' : 'default'}>{j.stalwart_result || '-'}</Badge></TableCell>
                <TableCell><Badge variant={j.dns_setup_status === 'completed' ? 'success' : 'warning'}>{j.dns_setup_status}</Badge></TableCell>
                <TableCell className="text-text-secondary text-xs max-w-[200px] truncate">{j.error_message || '-'}</TableCell>
                <TableCell className="text-text-secondary">{j.completed_at ? new Date(j.completed_at).toLocaleString() : '-'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
