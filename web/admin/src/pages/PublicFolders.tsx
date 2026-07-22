import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function PublicFoldersPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, name: '', email: '', description: '' })

  const { data, isLoading } = useQuery({ queryKey: ['public-folders'], queryFn: () => apiRequest<any>('/public-folders') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/public-folders', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['public-folders'] }); setShowCreate(false) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/public-folders/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['public-folders'] }),
  })

  if (isLoading) return <Loading className="h-64" />
  const folders = data?.public_folders || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Public Folders</h1>
        <Button onClick={() => setShowCreate(true)}>Create Folder</Button>
      </div>
      {folders.length === 0 ? (
        <EmptyState title="No public folders" description="Create shared folders accessible to multiple users." action={<Button onClick={() => setShowCreate(true)}>Create Folder</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Name</TableHeader><TableHeader>Email</TableHeader><TableHeader>Domain</TableHeader><TableHeader>Active</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {folders.map((f: any) => (
              <TableRow key={f.id}>
                <TableCell className="font-medium">{f.name}</TableCell>
                <TableCell className="text-xs">{f.email}</TableCell>
                <TableCell>{f.domain_id}</TableCell>
                <TableCell><Badge variant={f.is_active ? 'success' : 'default'}>{f.is_active ? 'Yes' : 'No'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(f.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Create Public Folder">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <Input label="Email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} required />
          <Input label="Description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
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
