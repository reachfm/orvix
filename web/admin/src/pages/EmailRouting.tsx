import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function EmailRoutingPage() {
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, action: 'forward', target: '' })

  const { data, isLoading } = useQuery({ queryKey: ['routing-rules'], queryFn: () => apiRequest<any>('/routing-rules') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/routing-rules', { method: 'POST', body: { ...body, name: 'Email Routing', condition: 'catch_all', priority: 0 } }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['routing-rules'] }); setShowCreate(false) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/routing-rules/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['routing-rules'] }),
  })

  const queryClient = useQueryClient()

  if (isLoading) return <Loading className="h-64" />
  const rules = data?.routing_rules || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Email Routing</h1>
        <Button onClick={() => setShowCreate(true)}>Add Forwarding</Button>
      </div>

      <Card className="mb-6">
        <p className="text-text-secondary text-sm">Configure email routing, forwarding, and catch-all addresses for your domains.</p>
      </Card>

      {rules.length === 0 ? (
        <EmptyState title="No routing rules" description="Add forwarding rules to route email to external addresses." action={<Button onClick={() => setShowCreate(true)}>Add Forwarding</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Domain</TableHeader>
              <TableHeader>Action</TableHeader>
              <TableHeader>Target</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {rules.map((r: any) => (
              <TableRow key={r.id}>
                <TableCell>{r.domain_id}</TableCell>
                <TableCell><Badge>{r.action}</Badge></TableCell>
                <TableCell className="text-xs">{r.target}</TableCell>
                <TableCell><Badge variant={r.is_enabled ? 'success' : 'default'}>{r.is_enabled ? 'Active' : 'Disabled'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(r.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Forwarding">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Domain ID" type="number" value={form.domain_id} onChange={(e) => setForm({ ...form, domain_id: parseInt(e.target.value) })} />
          <div>
            <label className="mb-1.5 block text-sm font-medium text-text-secondary">Action</label>
            <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="forward">Forward</option>
              <option value="catch_all">Catch-All</option>
            </select>
          </div>
          <Input label="Target Email" type="email" value={form.target} onChange={(e) => setForm({ ...form, target: e.target.value })} placeholder="user@example.com" required />
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
