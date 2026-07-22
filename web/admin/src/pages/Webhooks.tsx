import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function WebhooksPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ url: '', events: '', secret: '' })

  const { data, isLoading } = useQuery({
    queryKey: ['webhooks'],
    queryFn: () => apiRequest<any>('/webhooks'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/webhooks', { method: 'POST', body: { ...body, events: body.events.split(',').map((e: string) => e.trim()) } }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['webhooks'] }); setShowCreate(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/webhooks/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['webhooks'] }),
  })

  if (isLoading) return <Loading className="h-64" />

  const webhooks = data?.webhooks || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Webhooks</h1>
        <Button onClick={() => setShowCreate(true)}>Add Webhook</Button>
      </div>

      {webhooks.length === 0 ? (
        <EmptyState title="No webhooks" description="Create webhooks to receive event notifications." action={<Button onClick={() => setShowCreate(true)}>Add Webhook</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>URL</TableHeader>
              <TableHeader>Events</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Created</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {webhooks.map((w: any) => (
              <TableRow key={w.id}>
                <TableCell>{w.id}</TableCell>
                <TableCell className="font-medium text-xs max-w-[250px] truncate">{w.url}</TableCell>
                <TableCell className="text-text-secondary text-xs">{w.events}</TableCell>
                <TableCell><Badge variant={w.active ? 'success' : 'default'}>{w.active ? 'Active' : 'Inactive'}</Badge></TableCell>
                <TableCell className="text-text-secondary text-xs">{new Date(w.created_at).toLocaleString()}</TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete this webhook?')) deleteMutation.mutate(w.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Webhook">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="URL" type="url" value={form.url} onChange={(e) => setForm({ ...form, url: e.target.value })} placeholder="https://example.com/webhook" required />
          <Input label="Events (comma-separated)" value={form.events} onChange={(e) => setForm({ ...form, events: e.target.value })} placeholder="domain.created,user.created" required />
          <Input label="Secret" value={form.secret} onChange={(e) => setForm({ ...form, secret: e.target.value })} placeholder="Optional signing secret" />
          {createMutation.isError && <p className="text-sm text-danger">{(createMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
