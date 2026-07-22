import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function UsersPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ email: '', password: '', name: '' })

  const { data, isLoading } = useQuery({
    queryKey: ['tenant-users'],
    queryFn: () => apiRequest<any>('/admin/v1/tenant/users'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/admin/v1/tenant/users', { method: 'POST', body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tenant-users'] })
      setShowCreate(false)
      setForm({ email: '', password: '', name: '' })
    },
  })

  if (isLoading) return <Loading className="h-64" />

  const users = data?.users || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Users</h1>
        <Button onClick={() => setShowCreate(true)}>Create User</Button>
      </div>

      <Card className="mb-6">
        <p className="text-text-secondary text-sm">Tenant Admin — manage users within your organization. New users are automatically provisioned in the mail engine.</p>
      </Card>

      {users.length === 0 ? (
        <EmptyState title="No users" description="Create your first user." action={<Button onClick={() => setShowCreate(true)}>Create User</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Email</TableHeader>
              <TableHeader>Name</TableHeader>
              <TableHeader>Role</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Created</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {users.map((u: any) => (
              <TableRow key={u.id}>
                <TableCell>{u.id}</TableCell>
                <TableCell className="font-medium">{u.email}</TableCell>
                <TableCell className="text-text-secondary">{u.username || '-'}</TableCell>
                <TableCell><Badge variant={u.role === 'tenant_admin' ? 'info' : u.role === 'admin' ? 'warning' : 'default'}>{u.role}</Badge></TableCell>
                <TableCell><Badge variant={u.is_active ? 'success' : 'danger'}>{u.is_active ? 'Active' : 'Inactive'}</Badge></TableCell>
                <TableCell className="text-text-secondary">{new Date(u.created_at).toLocaleDateString()}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Create User">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} placeholder="user@acme.com" required />
          <Input label="Password" type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} placeholder="Secure password" required />
          <Input label="Display Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="John Doe" />
          {createMutation.isError && <p className="text-sm text-danger">{(createMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create User</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
