import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function MigrationPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [form, setForm] = useState({ source: 'generic_imap', source_host: '', source_port: 993, username: '', password: '', domain_id: 1 })

  const { data, isLoading } = useQuery({
    queryKey: ['migrations'],
    queryFn: () => apiRequest<any>('/migration/status'),
  })

  const startMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/migration/start', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['migrations'] }); setShowCreate(false) },
  })

  if (isLoading) return <Loading className="h-64" />

  const migrations = data?.migrations || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Migration Tool</h1>
        <Button onClick={() => setShowCreate(true)}>Start Migration</Button>
      </div>

      <Card className="mb-6">
        <p className="text-text-secondary text-sm">Migrate from existing mail servers to OrvixEM. Supports Axigen, Zimbra, Exchange, cPanel, Google Workspace, and any IMAP server.</p>
        <div className="mt-4 grid grid-cols-2 md:grid-cols-4 gap-3">
          {['Axigen', 'Zimbra', 'Exchange', 'cPanel', 'Google Workspace', 'Generic IMAP'].map((s) => (
            <div key={s} className="bg-bg-elevated rounded-lg px-3 py-2 text-sm text-text-primary text-center">{s}</div>
          ))}
        </div>
      </Card>

      {migrations.length === 0 ? (
        <EmptyState title="No migrations" description="Start a migration from your existing mail server." action={<Button onClick={() => setShowCreate(true)}>Start Migration</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Source</TableHeader>
              <TableHeader>Host</TableHeader>
              <TableHeader>Domain</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Progress</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {migrations.map((m: any) => (
              <TableRow key={m.id}>
                <TableCell className="font-medium">{m.source}</TableCell>
                <TableCell className="text-text-secondary">{m.source_host}</TableCell>
                <TableCell>{m.domain_id}</TableCell>
                <TableCell><Badge variant="info">Running</Badge></TableCell>
                <TableCell><div className="h-2 w-32 bg-bg-elevated rounded-full"><div className="h-full bg-accent rounded-full" style={{ width: `${m.progress || 0}%` }} /></div></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Start Migration">
        <form onSubmit={(e) => { e.preventDefault(); startMutation.mutate(form) }} className="space-y-4">
          <div><label className="mb-1.5 block text-sm font-medium text-text-secondary">Source</label>
            <select value={form.source} onChange={(e) => setForm({ ...form, source: e.target.value })} className="w-full rounded-lg border border-border bg-bg-elevated px-4 py-2 text-sm text-text-primary">
              <option value="generic_imap">Generic IMAP</option><option value="axigen">Axigen</option><option value="zimbra">Zimbra</option><option value="exchange">Exchange</option><option value="cpanel">cPanel</option><option value="google">Google Workspace</option>
            </select></div>
          <Input label="Source Host" value={form.source_host} onChange={(e) => setForm({ ...form, source_host: e.target.value })} required />
          <Input label="Port" type="number" value={form.source_port} onChange={(e) => setForm({ ...form, source_port: parseInt(e.target.value) })} />
          <Input label="Username" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} required />
          <Input label="Password" type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} />
          <Input label="Domain ID" type="number" value={form.domain_id} onChange={(e) => setForm({ ...form, domain_id: parseInt(e.target.value) })} />
          {startMutation.isError && <p className="text-sm text-danger">{(startMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={startMutation.isPending}>Start Migration</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
