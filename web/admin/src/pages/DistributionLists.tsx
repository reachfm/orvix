import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function DistributionListsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [selectedDL, setSelectedDL] = useState<any>(null)
  const [form, setForm] = useState({ domain_id: 1, name: '', email: '', description: '', is_public: false })
  const [memberEmail, setMemberEmail] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['distribution-lists'],
    queryFn: () => apiRequest<any>('/distribution-lists'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/distribution-lists', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['distribution-lists'] }); setShowCreate(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/distribution-lists/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['distribution-lists'] }),
  })

  const addMemberMutation = useMutation({
    mutationFn: ({ id, email }: { id: number; email: string }) => apiRequest(`/distribution-lists/${id}/members`, { method: 'POST', body: { email } }),
    onSuccess: () => { setMemberEmail('') },
  })

  if (isLoading) return <Loading className="h-64" />

  const lists = data?.distribution_lists || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Distribution Lists</h1>
        <Button onClick={() => setShowCreate(true)}>Create List</Button>
      </div>

      {lists.length === 0 ? (
        <EmptyState title="No distribution lists" description="Create mailing lists for your domains." action={<Button onClick={() => setShowCreate(true)}>Create List</Button>} />
      ) : (
        <div className="space-y-4">
          {lists.map((dl: any) => (
            <Card key={dl.id} title={dl.name} description={`${dl.email} · ${dl.is_public ? 'Public' : 'Private'}`}>
              <Button variant="ghost" size="sm" onClick={() => setSelectedDL(selectedDL?.id === dl.id ? null : dl)}>
                {selectedDL?.id === dl.id ? 'Hide Members' : 'Show Members'}
              </Button>
              <Button variant="danger" size="sm" className="ml-2" onClick={() => { if (confirm('Delete this list?')) deleteMutation.mutate(dl.id) }}>Delete</Button>
              
              {selectedDL?.id === dl.id && (
                <div className="mt-4">
                  <div className="flex gap-2 mb-3">
                    <Input placeholder="member@example.com" value={memberEmail} onChange={(e) => setMemberEmail(e.target.value)} />
                    <Button size="sm" onClick={() => { if (memberEmail) addMemberMutation.mutate({ id: dl.id, email: memberEmail }) }}>Add</Button>
                  </div>
                  <p className="text-text-secondary text-sm">Click "Show Members" and add members above.</p>
                </div>
              )}
            </Card>
          ))}
        </div>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Create Distribution List">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <Input label="Email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} required />
          <Input label="Description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          <Input label="Domain ID" type="number" value={form.domain_id} onChange={(e) => setForm({ ...form, domain_id: parseInt(e.target.value) })} />
          <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={form.is_public} onChange={(e) => setForm({ ...form, is_public: e.target.checked })} className="rounded border-border" /> Public list</label>
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
