import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function TenantsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ name: '', plan: 'trial', email: '', password: '' })

  const { data, isLoading } = useQuery({
    queryKey: ['admin-tenants'],
    queryFn: () => apiRequest<any>('/admin/v1/super/tenants'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/admin/v1/super/tenants', { method: 'POST', body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['admin-tenants'] })
      setShowCreate(false)
      setForm({ name: '', plan: 'trial', email: '', password: '' })
    },
  })

  if (isLoading) return <Loading className="h-64" />

  const tenants = data?.tenants || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Tenants</h1>
        <Button onClick={() => setShowCreate(true)}>Create Tenant</Button>
      </div>

      <Card className="mb-6">
        <p className="text-text-secondary text-sm">Super Admin — manage all organizations on the platform.</p>
      </Card>

      {tenants.length === 0 ? (
        <EmptyState title="No tenants" description="Create your first tenant organization." action={<Button onClick={() => setShowCreate(true)}>Create Tenant</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Name</TableHeader>
              <TableHeader>Plan / Tier</TableHeader>
              <TableHeader>Max Domains</TableHeader>
              <TableHeader>Max Mailboxes</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Created</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {tenants.map((t: any) => (
              <TableRow key={t.id}>
                <TableCell>{t.id}</TableCell>
                <TableCell className="font-medium">{t.name}</TableCell>
                <TableCell><Badge variant={t.tier === 'enterprise' ? 'info' : t.tier === 'isp' ? 'warning' : 'default'}>{t.tier}</Badge></TableCell>
                <TableCell>{t.max_domains}</TableCell>
                <TableCell>{t.max_mailboxes}</TableCell>
                <TableCell><Badge variant={t.active ? 'success' : 'danger'}>{t.active ? 'Active' : 'Inactive'}</Badge></TableCell>
                <TableCell className="text-text-secondary">{new Date(t.created_at).toLocaleDateString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Create Tenant">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Tenant Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="Acme Corp" required />
          <div>
            <label className="mb-1.5 block text-sm font-medium text-text-secondary">Plan</label>
            <select value={form.plan} onChange={(e) => setForm({ ...form, plan: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="trial">Trial (1 domain, 10 mailboxes)</option>
              <option value="starter">Starter (3 domains, 50 mailboxes)</option>
              <option value="pro">Pro (50 domains, 5000 mailboxes)</option>
              <option value="enterprise">Enterprise (1000 domains, 100k mailboxes)</option>
            </select>
          </div>
          <Input label="Admin Email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder="admin@acme.com" required />
          <Input label="Admin Password" type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} placeholder="Secure password" required />
          {createMutation.isError && <p className="text-sm text-danger">{(createMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create Tenant</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
