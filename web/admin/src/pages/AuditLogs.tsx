import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, EmptyState, Loading } from '@shared'

export function AuditLogsPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['audit-logs'],
    queryFn: () => apiRequest<any>('/admin/audit-logs'),
  })

  if (isLoading) return <Loading className="h-64" />

  const logs = data?.audit_logs || []

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Audit Logs</h1>

      {logs.length === 0 ? (
        <EmptyState title="No audit logs" description="Audit logs appear when administrative actions are taken." />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Action</TableHeader>
              <TableHeader>Resource</TableHeader>
              <TableHeader>Resource ID</TableHeader>
              <TableHeader>User</TableHeader>
              <TableHeader>IP</TableHeader>
              <TableHeader>Details</TableHeader>
              <TableHeader>Timestamp</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {logs.map((l: any) => (
              <TableRow key={l.id}>
                <TableCell>{l.id}</TableCell>
                <TableCell><Badge>{l.action}</Badge></TableCell>
                <TableCell className="text-text-secondary">{l.resource}</TableCell>
                <TableCell>{l.resource_id}</TableCell>
                <TableCell>{l.user_id ? `#${l.user_id}` : '-'}</TableCell>
                <TableCell className="font-mono text-xs">{l.ip}</TableCell>
                <TableCell className="text-text-secondary text-xs max-w-[250px] truncate">{l.details}</TableCell>
                <TableCell className="text-text-secondary text-xs">{new Date(l.created_at).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
