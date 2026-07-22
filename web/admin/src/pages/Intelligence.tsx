import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Badge, Loading } from '@shared'

export function IntelligencePage() {
  const { data, isLoading } = useQuery({ queryKey: ['intelligence'], queryFn: () => apiRequest<any>('/intelligence') })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Email Intelligence</h1>

      {data?.delivery_trends && (
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
          <Card><p className="text-text-secondary text-sm">Sent (24h)</p><p className="text-2xl font-bold text-text-primary mt-1">{data.delivery_trends.sent_24h}</p></Card>
          <Card><p className="text-text-secondary text-sm">Delivered (24h)</p><p className="text-2xl font-bold text-success mt-1">{data.delivery_trends.delivered_24h}</p></Card>
          <Card><p className="text-text-secondary text-sm">Bounced (24h)</p><p className="text-2xl font-bold text-danger mt-1">{data.delivery_trends.bounced_24h}</p></Card>
        </div>
      )}

      {data?.best_send_times && (
        <Card title="Best Send Times" className="mb-6">
          <div className="space-y-2">
            {Object.entries(data.best_send_times).map(([day, time]: [string, any]) => (
              <div key={day} className="flex justify-between text-sm">
                <span className="text-text-secondary capitalize">{day}</span>
                <span className="text-text-primary font-mono">{time}</span>
              </div>
            ))}
          </div>
        </Card>
      )}

      <Card title="Anomalies">
        {(data?.anomalies || []).length === 0 ? (
          <p className="text-text-secondary py-4 text-center">No anomalies detected.</p>
        ) : (
          <div className="space-y-2">
            {data.anomalies.map((a: any, i: number) => (
              <div key={i} className="flex items-center gap-3 p-2 rounded bg-bg-elevated">
                <Badge variant={a.severity === 'high' ? 'danger' : 'warning'}>{a.severity}</Badge>
                <span className="text-sm text-text-primary">{a.message}</span>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  )
}
