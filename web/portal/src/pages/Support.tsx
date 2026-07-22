import { Card } from '@shared'

export function SupportPage() {
  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Support</h1>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <Card title="Contact Us">
          <div className="space-y-4 mt-2">
            <p className="text-sm text-text-secondary">Need help? Reach out to our support team.</p>
            <div className="bg-bg-elevated rounded-lg p-4">
              <p className="text-sm text-text-primary"><strong>Email:</strong> support@orvix.email</p>
              <p className="text-sm text-text-primary mt-1"><strong>Documentation:</strong> docs.orvix.email</p>
              <p className="text-sm text-text-primary mt-1"><strong>Status:</strong> status.orvix.email</p>
            </div>
          </div>
        </Card>
        <Card title="SLA">
          <div className="space-y-3 mt-2">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Response Time</span><span className="text-text-primary">&lt; 4 hours</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Support Hours</span><span className="text-text-primary">24/7</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Uptime SLA</span><span className="text-text-primary">99.9%</span></div>
          </div>
        </Card>
      </div>
    </div>
  )
}
