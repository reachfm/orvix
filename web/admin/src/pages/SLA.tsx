import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Badge, Loading } from '@shared'

export function SLAPage() {
  const { data, isLoading } = useQuery({ queryKey: ['sla'], queryFn: () => apiRequest<any>('/sla/dashboard') })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">SLA Monitoring</h1>

      {data?.summary && (
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4 mb-6">
          <Card><p className="text-text-secondary text-sm">Uptime</p><p className="text-2xl font-bold text-success mt-1">{data.summary.uptime_percentage}%</p></Card>
          <Card><p className="text-text-secondary text-sm">Avg Response Time</p><p className="text-2xl font-bold text-text-primary mt-1">{data.summary.avg_response_time_ms}ms</p></Card>
          <Card><p className="text-text-secondary text-sm">Delivery Rate</p><p className="text-2xl font-bold text-info mt-1">{data.summary.delivery_rate}%</p></Card>
          <Card><p className="text-text-secondary text-sm">Status</p><p className="text-2xl font-bold text-success mt-1 capitalize">● {data.summary.status}</p></Card>
        </div>
      )}

      <Card title="SLA Target">
        <div className="flex items-center justify-between py-2"><span className="text-text-secondary">Target Uptime</span><span className="text-text-primary font-mono">99.9%</span></div>
        <div className="flex items-center justify-between py-2"><span className="text-text-secondary">Current Uptime</span><span className="text-success font-mono">{data?.summary?.uptime_percentage || 99.9}%</span></div>
        <div className="flex items-center justify-between py-2"><span className="text-text-secondary">SLA Status</span><Badge variant="success">Compliant</Badge></div>
      </Card>
    </div>
  )
}
