import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function GeoBlockingPage() {
  const queryClient = useQueryClient()
  const [showCreate, setShowCreate] = useState(false)
  const [country, setCountry] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['geo-blocks'],
    queryFn: () => apiRequest<any>('/firewall/geo'),
  })

  const createMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/firewall/geo', { method: 'POST', body }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ['geo-blocks'] }); setShowCreate(false); setCountry('') },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: number) => apiRequest(`/firewall/geo/${id}`, { method: 'DELETE' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['geo-blocks'] }),
  })

  if (isLoading) return <Loading className="h-64" />

  const blocks = data?.geo_blocks || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Geo-Blocking</h1>
        <Button onClick={() => setShowCreate(true)}>Block Country</Button>
      </div>

      {blocks.length === 0 ? (
        <EmptyState title="No geo blocks" description="No countries are currently blocked." action={<Button onClick={() => setShowCreate(true)}>Block Country</Button>} />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Country</TableHeader>
              <TableHeader>Status</TableHeader>
              <TableHeader>Reason</TableHeader>
              <TableHeader>Actions</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {blocks.map((b: any) => (
              <TableRow key={b.id}>
                <TableCell className="font-medium">{b.country}</TableCell>
                <TableCell><Badge variant={b.blocked ? 'danger' : 'success'}>{b.blocked ? 'Blocked' : 'Allowed'}</Badge></TableCell>
                <TableCell className="text-text-secondary">{b.reason || '-'}</TableCell>
                <TableCell><Button variant="secondary" size="sm" onClick={() => deleteMutation.mutate(b.id)}>Remove</Button></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Block Country">
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate({ country, blocked: true }) }} className="space-y-4">
          <Input label="Country Code (ISO 3166-1 alpha-2)" value={country} onChange={(e) => setCountry(e.target.value.toUpperCase())} placeholder="XX" maxLength={2} required />
          {createMutation.isError && <p className="text-sm text-danger">{(createMutation.error as Error).message}</p>}
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button type="submit" loading={createMutation.isPending}>Block</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
