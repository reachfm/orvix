import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function RoutingRulesPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, name: '', priority: 0, condition: '', action: 'forward', target: '' })

  const { data, isLoading } = useQuery({ queryKey: ['routing-rules'], queryFn: () => apiRequest<any>('/routing-rules') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/routing-rules', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['routing-rules'] }); setShowCreate(false) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/routing-rules/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['routing-rules'] }),
  })

  if (isLoading) return <Loading className="h-64" />
  const rules = data?.routing_rules || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Routing Rules</h1>
        <Button onClick={() => setShowCreate(true)}>Add Rule</Button>
      </div>
      {rules.length === 0 ? (
        <EmptyState title="No routing rules" description="Configure email routing rules for your domains." action={<Button onClick={() => setShowCreate(true)}>Add Rule</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Name</TableHeader><TableHeader>Condition</TableHeader><TableHeader>Action</TableHeader><TableHeader>Target</TableHeader><TableHeader>Priority</TableHeader><TableHeader>Status</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {rules.map((r: any) => (
              <TableRow key={r.id}>
                <TableCell className="font-medium">{r.name}</TableCell>
                <TableCell className="text-xs max-w-[200px] truncate">{r.condition}</TableCell>
                <TableCell><Badge>{r.action}</Badge></TableCell>
                <TableCell className="text-xs">{r.target}</TableCell>
                <TableCell>{r.priority}</TableCell>
                <TableCell><Badge variant={r.is_enabled ? 'success' : 'default'}>{r.is_enabled ? 'Active' : 'Disabled'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(r.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Routing Rule">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <Input label="Condition" value={form.condition} onChange={(e) => setForm({ ...form, condition: e.target.value })} placeholder="sender.domain == 'example.com'" required />
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Action</label>
            <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="forward">Forward</option><option value="redirect">Redirect</option><option value="reject">Reject</option><option value="bounce">Bounce</option>
            </select></div>
          <Input label="Target" value={form.target} onChange={(e) => setForm({ ...form, target: e.target.value })} placeholder="target@example.com" required />
          <Input label="Priority" type="number" value={form.priority} onChange={(e) => setForm({ ...form, priority: parseInt(e.target.value) })} />
          <Input label="Domain ID" type="number" value={form.domain_id} onChange={(e) => setForm({ ...form, domain_id: parseInt(e.target.value) })} />
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
