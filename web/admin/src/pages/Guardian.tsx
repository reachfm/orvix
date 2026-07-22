import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Badge, Loading } from '@shared'

export function GuardianPage() {
  const { data, isLoading } = useQuery({
    queryKey: ['guardian'],
    queryFn: () => apiRequest<any>('/guardian/status'),
  })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Guardian AI</h1>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-6">
        <Card><p className="text-text-secondary text-sm">Status</p><p className="text-xl font-bold text-success mt-1">● Active</p></Card>
        <Card><p className="text-text-secondary text-sm">Mode</p><p className="text-xl font-bold text-text-primary mt-1 capitalize">{data?.mode || 'local'}</p></Card>
      </div>

      <Card title="Recent Threat Analysis">
        <p className="text-text-secondary py-8 text-center">No threats analyzed yet. When threats are detected, they will appear here.</p>
      </Card>
    </div>
  )
}
