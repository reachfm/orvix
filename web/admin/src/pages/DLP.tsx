import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function DLPPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, name: '', description: '', pattern: '', action: 'block', severity: 'medium' })
  const [tab, setTab] = useState<'policies' | 'violations'>('policies')

  const { data: policies, isLoading: pLoading } = useQuery({ queryKey: ['dlp-policies'], queryFn: () => apiRequest<any>('/dlp/policies') })
  const { data: violations, isLoading: vLoading } = useQuery({ queryKey: ['dlp-violations'], queryFn: () => apiRequest<any>('/dlp/violations') })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/dlp/policies', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['dlp-policies'] }); setShowCreate(false) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/dlp/policies/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['dlp-policies'] }),
  })

  if (pLoading || vLoading) return <Loading className="h-64" />
  const pList = policies?.dlp_policies || []
  const vList = violations?.dlp_violations || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Data Loss Prevention</h1>
        <Button onClick={() => setShowCreate(true)}>Add Policy</Button>
      </div>

      <div className="flex gap-2 mb-6">
        <Button variant={tab === 'policies' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('policies')}>Policies ({pList.length})</Button>
        <Button variant={tab === 'violations' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('violations')}>Violations ({vList.length})</Button>
      </div>

      {tab === 'policies' && (pList.length === 0 ? (
        <EmptyState title="No DLP policies" description="Create policies to prevent data loss." action={<Button onClick={() => setShowCreate(true)}>Add Policy</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Name</TableHeader><TableHeader>Pattern</TableHeader><TableHeader>Action</TableHeader><TableHeader>Severity</TableHeader><TableHeader>Status</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {pList.map((p: any) => (
              <TableRow key={p.id}>
                <TableCell className="font-medium">{p.name}</TableCell>
                <TableCell className="font-mono text-xs max-w-[200px] truncate">{p.pattern}</TableCell>
                <TableCell><Badge variant={p.action === 'block' ? 'danger' : 'warning'}>{p.action}</Badge></TableCell>
                <TableCell><Badge variant={p.severity === 'high' || p.severity === 'critical' ? 'danger' : 'warning'}>{p.severity}</Badge></TableCell>
                <TableCell><Badge variant={p.is_enabled ? 'success' : 'default'}>{p.is_enabled ? 'Active' : 'Disabled'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(p.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ))}

      {tab === 'violations' && (vList.length === 0 ? (
        <EmptyState title="No violations" description="DLP violations appear when policies are triggered." />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader><TableHeader>Sender</TableHeader><TableHeader>Recipient</TableHeader><TableHeader>Action</TableHeader><TableHeader>Details</TableHeader><TableHeader>Timestamp</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {vList.map((v: any) => (
              <TableRow key={v.id}>
                <TableCell>{v.id}</TableCell>
                <TableCell className="text-xs">{v.sender_email}</TableCell>
                <TableCell className="text-xs">{v.recipient}</TableCell>
                <TableCell><Badge variant={v.action === 'block' ? 'danger' : 'warning'}>{v.action}</Badge></TableCell>
                <TableCell className="text-text-secondary text-xs max-w-[200px] truncate">{v.details}</TableCell>
                <TableCell className="text-text-secondary text-xs">{new Date(v.created_at).toLocaleString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      ))}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add DLP Policy">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <Input label="Description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          <Input label="Pattern (regex)" value={form.pattern} onChange={(e) => setForm({ ...form, pattern: e.target.value })} placeholder="\d{3}-\d{2}-\d{4}" required />
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Action</label>
            <select value={form.action} onChange={(e) => setForm({ ...form, action: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="block">Block</option><option value="quarantine">Quarantine</option><option value="alert">Alert Only</option>
            </select></div>
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Severity</label>
            <select value={form.severity} onChange={(e) => setForm({ ...form, severity: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="low">Low</option><option value="medium">Medium</option><option value="high">High</option><option value="critical">Critical</option>
            </select></div>
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
