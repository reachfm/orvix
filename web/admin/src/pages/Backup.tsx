import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, EmptyState, Loading } from '@shared'

export function BackupPage() {
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['backups'],
    queryFn: () => apiRequest<any>('/backups'),
  })

  const createMutation = useMutation({
    mutationFn: () => apiRequest('/backups', { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['backups'] }),
  })

  const restoreMutation = useMutation({
    mutationFn: (id: string) => apiRequest(`/backups/${id}/restore`, { method: 'POST' }),
  })

  if (isLoading) return <Loading className="h-64" />

  const backups = data?.backups || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Backup & Restore</h1>
        <Button onClick={() => createMutation.mutate()} loading={createMutation.isPending}>Create Backup</Button>
      </div>

      <Card className="mb-6">
        <p className="text-text-secondary text-sm mb-4">Create full backups of your OrvixEM configuration, database, and mail data. Backups can be stored locally or on S3-compatible storage.</p>
        <div className="grid grid-cols-3 gap-4">
          <div className="bg-bg-elevated rounded-lg p-4 text-center"><p className="text-2xl font-bold text-text-primary">{backups.length}</p><p className="text-text-secondary text-sm">Total Backups</p></div>
          <div className="bg-bg-elevated rounded-lg p-4 text-center"><p className="text-2xl font-bold text-success">0</p><p className="text-text-secondary text-sm">Stored Locally</p></div>
          <div className="bg-bg-elevated rounded-lg p-4 text-center"><p className="text-2xl font-bold text-info">0</p><p className="text-text-secondary text-sm">Stored on S3</p></div>
        </div>
      </Card>

      {backups.length === 0 ? (
        <EmptyState title="No backups" description="Create your first backup to protect your data." action={<Button onClick={() => createMutation.mutate()} loading={createMutation.isPending}>Create Backup</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Type</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Created</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {backups.map((b: any) => (
              <TableRow key={b.id}>
                <TableCell>{b.id}</TableCell>
                <TableCell><Badge>{b.type}</Badge></TableCell>
                <TableCell><Badge variant="success">Available</Badge></TableCell>
                <TableCell className="text-text-secondary">{new Date(b.created_at).toLocaleString()}</TableCell>
                <TableCell><Button variant="secondary" size="sm" onClick={() => { if (confirm('Restore this backup?')) restoreMutation.mutate(b.id) }} loading={restoreMutation.isPending}>Restore</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
