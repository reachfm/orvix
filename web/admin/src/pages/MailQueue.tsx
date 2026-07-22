import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, EmptyState, Loading } from '@shared'

export function MailQueuePage() {
  const queryClient = useQueryClient()

  const { data: queue, isLoading } = useQuery({
    queryKey: ['mail-queue'],
    queryFn: () => apiRequest<any>('/mail-queue'),
  })

  const { data: stats } = useQuery({
    queryKey: ['mail-queue-stats'],
    queryFn: () => apiRequest<any>('/mail-queue/stats'),
  })

  const retryMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/mail-queue/${id}/retry`, { method: 'POST' }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['mail-queue'] }); queryClient.invalidateQueries({ queryKey: ['mail-queue-stats'] }) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/mail-queue/${id}`, { method: 'DELETE' }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['mail-queue'] }); queryClient.invalidateQueries({ queryKey: ['mail-queue-stats'] }) },
  })

  if (isLoading) return <Loading className="h-64" />

  const items = queue?.mail_queue || []

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Mail Queue</h1>

      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-5 gap-4 mb-6">
          <Card><p className="text-text-secondary text-sm">Total</p><p className="text-2xl font-bold text-text-primary">{stats.total}</p></Card>
          <Card><p className="text-text-secondary text-sm">Queued</p><p className="text-2xl font-bold text-text-primary">{stats.queued}</p></Card>
          <Card><p className="text-text-secondary text-sm">Sent</p><p className="text-2xl font-bold text-success">{stats.sent}</p></Card>
          <Card><p className="text-text-secondary text-sm">Failed</p><p className="text-2xl font-bold text-danger">{stats.failed}</p></Card>
          <Card><p className="text-text-secondary text-sm">Deferred</p><p className="text-2xl font-bold text-warning">{stats.deferred}</p></Card>
        </div>
      )}

      {items.length === 0 ? (
        <EmptyState title="Queue is empty" description="No messages in the mail queue." />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>From</TableHeader>
              <TableHeader>To</TableHeader>
              <TableHeader>Domain</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Attempts</TableHeader>
              <TableHeader>Error</TableHeader>
              <TableHeader>Created</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {items.map((item: any) => (
              <TableRow key={item.id}>
                <TableCell>{item.id}</TableCell>
                <TableCell className="font-medium text-xs">{item.from_addr}</TableCell>
                <TableCell className="text-xs">{item.to_addr}</TableCell>
                <TableCell className="text-text-secondary">{item.domain}</TableCell>
                <TableCell><Badge variant={item.status === 'sent' ? 'success' : item.status === 'failed' ? 'danger' : item.status === 'queued' ? 'info' : 'warning'}>{item.status}</Badge></TableCell>
                <TableCell>{item.attempts}</TableCell>
                <TableCell className="text-text-secondary text-xs max-w-[200px] truncate">{item.last_error || '-'}</TableCell>
                <TableCell className="text-text-secondary text-xs">{new Date(item.created_at).toLocaleString()}</TableCell>
                <TableCell>
                  <div className="flex gap-2">
                    {item.status === 'failed' && <Button variant="secondary" size="sm" onClick={() => retryMutation.mutate(item.id)}>Retry</Button>}
                    <Button variant="danger" size="sm" onClick={() => { if (confirm('Delete this item?')) deleteMutation.mutate(item.id) }}>Delete</Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
