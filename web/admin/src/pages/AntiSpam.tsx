import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function AntiSpamPage() {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<'whitelist' | 'blacklist'>('whitelist')
  const [showAdd, setShowAdd] = useState(false)
  const [form, setForm] = useState({ type: 'email', value: '', reason: '' })

  const { data: whitelist, isLoading: wlLoading } = useQuery({
    queryKey: ['spam-whitelist'],
    queryFn: () => apiRequest<any>('/spam/whitelist'),
  })

  const { data: blacklist, isLoading: blLoading } = useQuery({
    queryKey: ['spam-blacklist'],
    queryFn: () => apiRequest<any>('/spam/blacklist'),
  })

  const addMutation = useMutation({
    mutationFn: (body: any) => apiRequest(`/spam/${tab}`, { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: [`spam-${tab}`] }); setShowAdd(false) },
  })

  const deleteMutation = useMutation({
    mutationFn: ({ list, id }: { list: string; id: number }) => apiRequest(`/spam/${list}/${id}`, { method: 'DELETE' }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['spam-whitelist'] }); queryClient.invalidateQueries({ queryKey: ['spam-blacklist'] }) },
  })

  if (wlLoading || blLoading) return <Loading className="h-64" />

  const items = tab === 'whitelist' ? (whitelist?.items || []) : (blacklist?.items || [])

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Anti-Spam</h1>
        <Button onClick={() => setShowAdd(true)}>Add {tab === 'whitelist' ? 'Whitelist' : 'Blacklist'} Entry</Button>
      </div>

      <Card className="mb-6">
        <p className="text-text-secondary text-sm">Manage spam filtering whitelist and blacklist entries. Whitelisted senders always pass through. Blacklisted senders are always blocked.</p>
      </Card>

      <div className="flex gap-2 mb-6">
        <Button variant={tab === 'whitelist' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('whitelist')}>Whitelist ({(whitelist?.items || []).length})</Button>
        <Button variant={tab === 'blacklist' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('blacklist')}>Blacklist ({(blacklist?.items || []).length})</Button>
      </div>

      {items.length === 0 ? (
        <EmptyState title={`No ${tab} entries`} description={`${tab === 'whitelist' ? 'Whitelisted' : 'Blacklisted'} senders will appear here.`} action={<Button onClick={() => setShowAdd(true)}>Add Entry</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Type</TableHeader>
              <TableHeader>Value</TableHeader>
              <TableHeader>Reason</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {items.map((item: any) => (
              <TableRow key={item.id}>
                <TableCell><Badge>{item.type}</Badge></TableCell>
                <TableCell className="font-mono text-xs">{item.value}</TableCell>
                <TableCell className="text-text-secondary">{item.reason || '-'}</TableCell>
                <TableCell><Button variant="danger" size="sm" onClick={() => { if (confirm('Remove this entry?')) deleteMutation.mutate({ list: tab, id: item.id }) }}>Remove</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showAdd} onClose={() => setShowAdd(false)} title={`Add ${tab === 'whitelist' ? 'Whitelist' : 'Blacklist'} Entry`}>
        <form onSubmit={(e) => { e.preventDefault(); addMutation.mutate(form) }} className="space-y-4">
          <div>
            <label className="mb-1.5 block text-sm font-medium text-text-secondary">Type</label>
            <select value={form.type} onChange={(e) => setForm({ ...form, type: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="email">Email Address</option>
              <option value="domain">Domain</option>
              <option value="ip">IP Address</option>
            </select>
          </div>
          <Input label="Value" value={form.value} onChange={(e) => setForm({ ...form, value: e.target.value })} placeholder="user@example.com" required />
          <Input label="Reason" value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} placeholder="Optional reason" />
          {addMutation.isError && <p className="text-sm text-danger">{(addMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowAdd(false)}>Cancel</Button>
            <Button type="submit" loading={addMutation.isPending}>Add</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
