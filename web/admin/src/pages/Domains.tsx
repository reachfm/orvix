import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function DomainsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ name: '', tenant_id: 1 })

  const { data, isLoading } = useQuery({
    queryKey: ['domains'],
    queryFn: () => apiRequest<any>('/admin/domains'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/admin/domains', { method: 'POST', body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['domains'] })
      setShowCreate(false)
      setForm({ name: '', tenant_id: 1 })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/admin/domains/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['domains'] }),
  })

  if (isLoading) return <Loading className="h-64" />

  const domains = data?.domains || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Domains</h1>
        <Button onClick={() => setShowCreate(true)}>Add Domain</Button>
      </div>

      {domains.length === 0 ? (
        <EmptyState title="No domains" description="Add your first email domain." action={<Button onClick={() => setShowCreate(true)}>Add Domain</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Name</TableHeader>
              <TableHeader>Tenant</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>DKIM</TableHeader>
              <TableHeader>DMARC</TableHeader>
              <TableHeader>Created</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {domains.map((d: any) => (
              <TableRow key={d.id}>
                <TableCell>{d.id}</TableCell>
                <TableCell className="font-medium">{d.name}</TableCell>
                <TableCell>{d.tenant_id}</TableCell>
                <TableCell><Badge variant={d.status === 'active' ? 'success' : d.status === 'pending' ? 'warning' : 'danger'}>{d.status}</Badge></TableCell>
                <TableCell className="text-text-secondary text-xs max-w-[200px] truncate">{d.dkim_selector || '-'}</TableCell>
                <TableCell className="text-text-secondary">{d.dmarc_policy || '-'}</TableCell>
                <TableCell className="text-text-secondary">{new Date(d.created_at).toLocaleDateString()}</TableCell>
                <TableCell>
                  <Button variant="danger" size="sm" onClick={() => { if (confirm('Delete this domain?')) deleteMutation.mutate(d.id) }}>Delete</Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Domain">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Domain Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="example.com" required />
          <Input label="Tenant ID" type="number" value={form.tenant_id} onChange={(e) => setForm({ ...form, tenant_id: parseInt(e.target.value) })} />
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
