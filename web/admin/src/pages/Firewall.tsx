import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function FirewallPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ name: '', field: 'ip', operator: 'equals', value: '', action: 'block', priority: 0, enabled: true })

  const { data, isLoading } = useQuery({
    queryKey: ['firewall-rules'],
    queryFn: () => apiRequest<any>('/firewall/rules'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/firewall/rules', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['firewall-rules'] }); setShowCreate(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/firewall/rules/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['firewall-rules'] }),
  })

  if (isLoading) return <Loading className="h-64" />

  const rules = data?.firewall_rules || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Firewall Rules</h1>
        <Button onClick={() => setShowCreate(true)}>Add Rule</Button>
      </div>

      {rules.length === 0 ? (
        <EmptyState title="No firewall rules" description="Add rules to protect your mail infrastructure." action={<Button onClick={() => setShowCreate(true)}>Add Rule</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Name</TableHeader>
              <TableHeader>Field</TableHeader>
              <TableHeader>Operator</TableHeader>
              <TableHeader>Value</TableHeader>
              <TableHeader>Action</TableHeader>
              <TableHeader>Priority</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {rules.map((r: any) => (
              <TableRow key={r.id}>
                <TableCell className="font-medium">{r.name}</TableCell>
                <TableCell className="font-mono text-xs">{r.field}</TableCell>
                <TableCell className="text-text-secondary">{r.operator}</TableCell>
                <TableCell className="font-mono text-xs">{r.value}</TableCell>
                <TableCell><Badge variant={r.action === 'block' ? 'danger' : r.action === 'quarantine' ? 'warning' : 'success'}>{r.action}</Badge></TableCell>
                <TableCell>{r.priority}</TableCell>
                <TableCell><Badge variant={r.enabled ? 'success' : 'default'}>{r.enabled ? 'Active' : 'Disabled'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete this rule?')) deleteMutation.mutate(r.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Firewall Rule">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Field</label>
            <select value={form.field} onChange={(e) => setForm({ ...form, field: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="ip">IP</option><option value="country">Country</option><option value="ehlo">EHLO</option><option value="mail_from">Mail From</option><option value="protocol">Protocol</option><option value="auth_user">Auth User</option>
            </select></div>
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Operator</label>
            <select value={form.operator} onChange={(e) => setForm({ ...form, operator: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="equals">Equals</option><option value="contains">Contains</option><option value="prefix">Prefix</option><option value="suffix">Suffix</option><option value="not_equals">Not Equals</option>
            </select></div>
          <Input label="Value" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} required />
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Action</label>
            <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="block">Block</option><option value="quarantine">Quarantine</option><option value="allow">Allow</option>
            </select></div>
          <Input label="Priority" type="number" value={form.priority} onChange={(e) => setForm({ ...form, priority: parseInt(e.target.value) })} />
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
