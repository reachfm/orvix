import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { apiRequest, Card, Button, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Loading } from '@shared'

export function AutoHealPage() {
  const queryClient = useQueryClient()

  const { data, isLoading } = useQuery({
    queryKey: ['autoheal'],
    queryFn: () => apiRequest<any>('/autoheal/status'),
  })

  const triggerMutation = useMutation({
    mutationFn: () => apiRequest('/autoheal/trigger', { method: 'POST' }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['autoheal'] }),
  })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold text-text-primary">Auto-Heal</h1>
        <Button onClick={() => triggerMutation.mutate()} loading={triggerMutation.isPending}>Trigger Heal</Button>
      </div>

      <Card title="Service Health" className="mb-6">
        <div className="space-y-3">
          {data?.health && Object.entries(data.health).map(([service, status]: [string, any]) => (
            <div key={service} className="flex justify-between items-center">
              <span className="text-text-secondary capitalize">{service}</span>
              <Badge variant={status === 'ok' ? 'success' : status === 'warning' ? 'warning' : 'danger'}>{status}</Badge>
            </div>
          ))}
        </div>
      </Card>

      <Card title="Heal History">
        <p className="text-text-secondary py-8 text-center">No heal actions recorded yet.</p>
      </Card>
    </div>
  )
}
