import { Card } from '@shared'

export function BillingPage() {
  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Billing</h1>
      <Card title="Payment Provider" className="mb-6">
        <div className="bg-warning/10 border border-warning/20 rounded-lg p-4 mb-4">
          <p className="text-sm font-medium text-warning">⚠️ Payment provider not yet configured.</p>
          <p className="text-sm text-text-secondary mt-1">Billing integration is coming soon. You will be able to manage invoices and payment methods here.</p>
        </div>
        <div className="space-y-3">
          <div className="flex justify-between text-sm"><span className="text-text-secondary">Current Plan</span><span className="text-text-primary">SMB</span></div>
          <div className="flex justify-between text-sm"><span className="text-text-secondary">Status</span><span className="text-text-secondary">Active (grace period)</span></div>
          <div className="flex justify-between text-sm"><span className="text-text-secondary">Next Invoice</span><span className="text-text-secondary">N/A — provider not configured</span></div>
        </div>
      </Card>
    </div>
  )
}
