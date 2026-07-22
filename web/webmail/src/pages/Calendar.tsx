import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Modal, EmptyState, Loading } from '@shared'

export function CalendarPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ title: '', description: '', start_at: '', end_at: '', location: '' })

  const { data, isLoading } = useQuery({ queryKey: ['calendars'], queryFn: () => apiRequest<any>('/calendars') })
  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/events', { method: 'POST', body: { ...body, calendar_id: 1 } }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['calendars'] }); setShowCreate(false) },
  })

  if (isLoading) return <Loading className="h-64" />
  const calendars = data?.calendars || []

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Calendar</h1>
        <Button onClick={() => setShowCreate(true)}>Create Event</Button>
      </div>

      {calendars.length === 0 ? (
        <EmptyState title="No calendars" description="Create events and calendars." action={<Button onClick={() => setShowCreate(true)}>Create Event</Button>} />
      ) : (
        <div className="grid gap-4">
          {calendars.map((cal: any) => (
            <Card key={cal.id} title={cal.name}>
              <p className="text-text-secondary text-sm">Shared: {cal.is_shared ? 'Yes' : 'No'}</p>
            </Card>
          ))}
        </div>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Create Event">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(form) }} className="space-y-4">
          <Input label="Title" value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} required />
          <Input label="Description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          <Input label="Start" type="datetime-local" value={form.start_at} onChange={(e) => setForm({ ...form, start_at: e.target.value })} />
          <Input label="End" type="datetime-local" value={form.end_at} onChange={(e) => setForm({ ...form, end_at: e.target.value })} />
          <Input label="Location" value={form.location} onChange={(e) => setForm({ ...form, location: e.target.value })} />
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Create</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
