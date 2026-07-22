import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function LDAPPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, server_url: '', bind_dn: '', bind_password: '', base_dn: '', user_filter: '(objectClass=person)', sync_interval: 3600 })

  const { data, isLoading } = useQuery({ queryKey: ['ldap-configs'], queryFn: () => apiRequest<any>('/ldap/configs') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/ldap/configs', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['ldap-configs'] }); setShowCreate(false) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/ldap/configs/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['ldap-configs'] }),
  })
  const syncMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/ldap/configs/${id}/sync`, { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['ldap-configs'] }),
  })

  if (isLoading) return <Loading className="h-64" />
  const configs = data?.ldap_configs || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">LDAP / Active Directory</h1>
        <Button onClick={() => setShowCreate(true)}>Add Configuration</Button>
      </div>
      {configs.length === 0 ? (
        <EmptyState title="No LDAP configs" description="Connect to LDAP or Active Directory for user sync." action={<Button onClick={() => setShowCreate(true)}>Add Configuration</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Server</TableHeader><TableHeader>Bind DN</TableHeader><TableHeader>Base DN</TableHeader><TableHeader>Sync Interval</TableHeader><TableHeader>Last Sync</TableHeader><TableHeader>Status</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {configs.map((c: any) => (
              <TableRow key={c.id}>
                <TableCell className="font-mono text-xs">{c.server_url}</TableCell>
                <TableCell className="text-xs">{c.bind_dn}</TableCell>
                <TableCell className="text-xs">{c.base_dn}</TableCell>
                <TableCell>{c.sync_interval}s</TableCell>
                <TableCell className="text-text-secondary text-xs">{c.last_sync_at ? new Date(c.last_sync_at).toLocaleString() : 'Never'}</TableCell>
                <TableCell><Badge variant={c.is_active ? 'success' : 'default'}>{c.is_active ? 'Active' : 'Inactive'}</Badge></TableCell>
                <TableCell><div className="flex gap-2">
                  <Button variant="secondary" size="sm" onClick={() => syncMutation.mutate(c.id)} loading={syncMutation.isPending}>Sync</Button>
                  <Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(c.id) }}>Delete</Button>
                </div></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add LDAP Configuration">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Server URL" value={form.server_url} onChange={(e) => setForm({ ...form, server_url: e.target.value })} placeholder="ldap://ldap.example.com:389" required />
          <Input label="Bind DN" value={form.bind_dn} onChange={(e) => setForm({ ...form, bind_dn: e.target.value })} placeholder="cn=admin,dc=example,dc=com" required />
          <Input label="Bind Password" type="password" value={form.bind_password} onChange={(e) => setForm({ ...form, bind_password: e.target.value })} />
          <Input label="Base DN" value={form.base_dn} onChange={(e) => setForm({ ...form, base_dn: e.target.value })} placeholder="dc=example,dc=com" required />
          <Input label="User Filter" value={form.user_filter} onChange={(e) => setForm({ ...form, user_filter: e.target.value })} />
          <Input label="Sync Interval (seconds)" type="number" value={form.sync_interval} onChange={(e) => setForm({ ...form, sync_interval: parseInt(e.target.value) })} />
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
