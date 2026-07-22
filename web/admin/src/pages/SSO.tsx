import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function SSOPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, provider: 'saml', client_id: '', client_secret: '', metadata_url: '', entity_id: '', acs_endpoint: '', slo_endpoint: '' })

  const { data, isLoading } = useQuery({ queryKey: ['sso-configs'], queryFn: () => apiRequest<any>('/sso/configs') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/sso/configs', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['sso-configs'] }); setShowCreate(false) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/sso/configs/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['sso-configs'] }),
  })

  if (isLoading) return <Loading className="h-64" />
  const configs = data?.sso_configs || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Single Sign-On</h1>
        <Button onClick={() => setShowCreate(true)}>Add Configuration</Button>
      </div>
      {configs.length === 0 ? (
        <EmptyState title="No SSO configs" description="Configure SAML 2.0 or OAuth2 single sign-on." action={<Button onClick={() => setShowCreate(true)}>Add Configuration</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Provider</TableHeader><TableHeader>Client ID</TableHeader><TableHeader>Entity ID</TableHeader><TableHeader>ACS Endpoint</TableHeader><TableHeader>Status</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {configs.map((c: any) => (
              <TableRow key={c.id}>
                <TableCell><Badge>{c.provider}</Badge></TableCell>
                <TableCell className="font-mono text-xs">{c.client_id}</TableCell>
                <TableCell className="text-xs">{c.entity_id || '-'}</TableCell>
                <TableCell className="text-xs">{c.acs_endpoint || '-'}</TableCell>
                <TableCell><Badge variant={c.is_active ? 'success' : 'default'}>{c.is_active ? 'Active' : 'Inactive'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(c.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add SSO Configuration">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Provider</label>
            <select value={form.provider} onChange={(e) => setForm({ ...form, provider: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="saml">SAML 2.0</option><option value="oauth2">OAuth2 / OpenID Connect</option>
            </select></div>
          <Input label="Client ID" value={form.client_id} onChange={(e) => setForm({ ...form, client_id: e.target.value })} required />
          <Input label="Client Secret" type="password" value={form.client_secret} onChange={(e) => setForm({ ...form, client_secret: e.target.value })} />
          <Input label="Metadata URL" value={form.metadata_url} onChange={(e) => setForm({ ...form, metadata_url: e.target.value })} />
          <Input label="Entity ID" value={form.entity_id} onChange={(e) => setForm({ ...form, entity_id: e.target.value })} />
          <Input label="ACS Endpoint" value={form.acs_endpoint} onChange={(e) => setForm({ ...form, acs_endpoint: e.target.value })} />
          <Input label="SLO Endpoint" value={form.slo_endpoint} onChange={(e) => setForm({ ...form, slo_endpoint: e.target.value })} />
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
