import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Modal, EmptyState, Loading } from '@shared'

export function LogViewerPage() {
  const [tab, setTab] = useState<'smtp' | 'imap' | 'auth'>('smtp')
  const [search, setSearch] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['logs', tab, search],
    queryFn: () => apiRequest<any>(`/logs/${tab}?q=${encodeURIComponent(search)}`),
  })

  if (isLoading) return <Loading className="h-64" />

  const logs = data?.logs || []

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Log Viewer</h1>
      </div>

      <div className="flex gap-2 mb-4">
        {['smtp', 'imap', 'auth'].map((l) => (
          <Button key={l} variant={tab === l ? 'primary' : 'secondary'} size="sm" onClick={() => setTab(l as any)}>
            {l.toUpperCase()} Logs
          </Button>
        ))}
        <div className="flex-1" />
        <Input placeholder="Search logs..." value={search} onChange={(e) => setSearch(e.target.value)} className="max-w-xs" />
      </div>

      {logs.length === 0 ? (
        <EmptyState title={`No ${tab.toUpperCase()} logs`} description="Logs appear when Stalwart is connected and processing mail." />
      ) : (
        <Table>
          <TableHead>
            <TableRow>
              <TableHeader>Timestamp</TableHeader>
              <TableHeader>Level</TableHeader>
              <TableHeader>Message</TableHeader>
              <TableHeader>Source</TableHeader>
            </TableRow>
          </TableHead>
          <TableBody>
            {logs.map((l: any, i: number) => (
              <TableRow key={i}>
                <TableCell className="text-xs">{l.timestamp || '-'}</TableCell>
                <TableCell><Badge variant={l.level === 'error' ? 'danger' : l.level === 'warn' ? 'warning' : 'default'}>{l.level || 'info'}</Badge></TableCell>
                <TableCell className="text-xs font-mono max-w-[400px] truncate">{l.message || l}</TableCell>
                <TableCell className="text-xs">{l.source || '-'}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}
