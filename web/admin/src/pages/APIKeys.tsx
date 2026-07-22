import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function APIKeysPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [name, setName] = useState('')
  const [newKey, setNewKey] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['api-keys'],
    queryFn: () => apiRequest<any>('/api-keys'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/api-keys', { method: 'POST', body: { ...body, permissions: ['read'] } }),
    onSuccess: (res: any) => { queryClient.invalidateQueries({ queryKey: ['api-keys'] }); setNewKey(res.api_key); setName('') },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/api-keys/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['api-keys'] }),
  })

  if (isLoading) return <Loading className="h-64" />

  const keys = data?.api_keys || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">API Keys</h1>
        <Button onClick={() => setShowCreate(true)}>Generate Key</Button>
      </div>

      {newKey && (
        <Card className="mb-6 border-success/30 bg-success/5">
          <p className="text-sm font-medium text-success mb-2">API Key Generated — Copy it now. You won't see it again.</p>
          <div className="bg-bg-elevated rounded-lg p-3 font-mono text-xs break-all text-text-primary">{newKey}</div>
          <Button variant="secondary" size="sm" className="mt-2" onClick={() => { navigator.clipboard?.writeText(newKey) }}>Copy</Button>
        </Card>
      )}

      {keys.length === 0 ? (
        <EmptyState title="No API keys" description="Generate API keys for programmatic access." action={<Button onClick={() => setShowCreate(true)}>Generate Key</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>ID</TableHeader>
              <TableHeader>Name</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Last Used</TableHeader>
              <TableHeader>Created</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {keys.map((k: any) => (
              <TableRow key={k.id}>
                <TableCell>{k.id}</TableCell>
                <TableCell className="font-medium">{k.name}</TableCell>
                <TableCell><Badge variant={k.active ? 'success' : 'danger'}>{k.active ? 'Active' : 'Revoked'}</Badge></TableCell>
                <TableCell className="text-text-secondary text-xs">{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : 'Never'}</TableCell>
                <TableCell className="text-text-secondary text-xs">{new Date(k.created_at).toLocaleString()}</TableCell>
                <TableCell>{k.active && <Button variant="danger" size="sm" onClick={() => { if (confirm('Revoke this key?')) deleteMutation.mutate(k.id) }}>Revoke</Button>}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Generate API Key">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate({ name }) }} className="space-y-4">
          <Input label="Key Name" value={name} onChange={(e) => setName(e.target.value)} placeholder="My API Key" required />
          {createMutation.isError && <p className="text-sm text-danger">{(createMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Generate</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
