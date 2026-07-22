import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function ResourcesPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ domain_id: 1, name: '', email: '', type: 'room', capacity: 0, location: '' })

  const { data, isLoading } = useQuery({ queryKey: ['resources'], queryFn: () => apiRequest<any>('/resources') })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/resources', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['resources'] }); setShowCreate(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/resources/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['resources'] }),
  })

  if (isLoading) return <Loading className="h-64" />
  const resources = data?.resources || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Resources</h1>
        <Button onClick={() => setShowCreate(true)}>Add Resource</Button>
      </div>
      {resources.length === 0 ? (
        <EmptyState title="No resources" description="Add bookable resources like rooms and equipment." action={<Button onClick={() => setShowCreate(true)}>Add Resource</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Name</TableHeader><TableHeader>Email</TableHeader><TableHeader>Type</TableHeader><TableHeader>Capacity</TableHeader><TableHeader>Location</TableHeader><TableHeader>Status</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {resources.map((r: any) => (
              <TableRow key={r.id}>
                <TableCell className="font-medium">{r.name}</TableCell>
                <TableCell className="text-xs">{r.email}</TableCell>
                <TableCell><Badge>{r.type}</Badge></TableCell>
                <TableCell>{r.capacity}</TableCell>
                <TableCell className="text-text-secondary">{r.location || '-'}</TableCell>
                <TableCell><Badge variant={r.is_active ? 'success' : 'default'}>{r.is_active ? 'Active' : 'Inactive'}</Badge></TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(r.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Resource">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <Input label="Email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} required />
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Type</label>
            <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="room">Room</option><option value="equipment">Equipment</option><option value="vehicle">Vehicle</option><option value="other">Other</option>
            </select></div>
          <Input label="Capacity" type="number" value={form.capacity} onChange={(e) => setForm({ ...form, capacity: parseInt(e.target.value) })} />
          <Input label="Location" value={form.location} onChange={(e) => setForm({ ...form, location: e.target.value })} />
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
