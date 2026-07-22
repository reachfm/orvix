import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Modal, EmptyState, Loading } from '@shared'

export function ContactsPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ name: '', email: '', phone: '', company: '', notes: '' })

  const { data, isLoading } = useQuery({ queryKey: ['contacts'], queryFn: () => apiRequest<any>('/contacts') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/contacts', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['contacts'] }); setShowCreate(false); setForm({ name: '', email: '', phone: '', company: '', notes: '' }) },
  })
  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/contacts/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['contacts'] }),
  })

  if (isLoading) return <Loading className="h-64" />
  const contacts = data?.contacts || []

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Contacts</h1>
        <Button onClick={() => setShowCreate(true)}>Add Contact</Button>
      </div>
      {contacts.length === 0 ? (
        <EmptyState title="No contacts" description="Add contacts to your address book." action={<Button onClick={() => setShowCreate(true)}>Add Contact</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Name</TableHeader><TableHeader>Email</TableHeader><TableHeader>Phone</TableHeader><TableHeader>Company</TableHeader><TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {contacts.map((c: any) => (
              <TableRow key={c.id}>
                <TableCell className="font-medium">{c.name}</TableCell>
                <TableCell>{c.email}</TableCell>
                <TableCell className="text-text-secondary">{c.phone || '-'}</TableCell>
                <TableCell className="text-text-secondary">{c.company || '-'}</TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Delete?')) deleteMutation.mutate(c.id) }}>Delete</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Add Contact">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} required />
          <Input label="Email" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
          <Input label="Phone" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} />
          <Input label="Company" value={form.company} onChange={(e) => setForm({ ...form, company: e.target.value })} />
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Notes</label>
            <textarea value={form.notes} onChange={(e) => setForm({ ...form, notes: e.target.value })}
              className="w-full h-24 rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary outline-0 focus:border-accent focus:ring-2 focus:ring-accent/20 resize-none" /></div>
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Save</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
