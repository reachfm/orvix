import { useState } from 'react'
import { Card, Button, Badge } from '@shared'

export function MaintenancePage() {
  const [enabled, setEnabled] = useState(false)

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Maintenance Mode</h1>

      <Card className="mb-6">
        <div className="flex items-center justify-between">
          <div>
            <p className="text-lg font-semibold text-text-primary">Status</p>
            <p className="text-text-secondary text-sm mt-1">{enabled ? 'Maintenance mode is active' : 'System is operating normally'}</p>
          </div>
          <Badge variant={enabled ? 'warning' : 'success'}>{enabled ? 'Maintenance' : 'Online'}</Badge>
        </div>
        <div className="mt-6">
          <Button variant={enabled ? 'secondary' : 'danger'} onClick={() => setEnabled(!enabled)}>
            {enabled ? 'Disable Maintenance Mode' : 'Enable Maintenance Mode'}
          </Button>
        </div>
        {enabled && (
          <div className="mt-4 bg-warning/10 border border-warning/20 rounded-lg p-4">
            <p className="text-sm text-warning">⚠️ Maintenance mode is enabled. Users will see a maintenance banner on the webmail and portal.</p>
          </div>
        )}
      </Card>
    </div>
  )
}
