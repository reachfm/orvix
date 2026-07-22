import { useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { useAuthStore, apiRequest, Card, Button, Input, Badge, Loading } from '@shared'

export function SettingsPage() {
  const { user, token } = useAuthStore()
  const [tab, setTab] = useState<'profile' | '2fa' | 'sessions'>('profile')
  const [totpCode, setTotpCode] = useState('')
  const [totpSecret, setTotpSecret] = useState('')

  const { data: totpSetup, isLoading: totpLoading } = useQuery({
    queryKey: ['totp-setup'],
    queryFn: () => apiRequest<any>('/totp/setup'),
    enabled: tab === '2fa',
  })

  const enableTotpMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/totp/enable', { method: 'POST', body }),
    onSuccess: () => alert('2FA enabled!'),
  })

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-text-primary mb-6">Settings</h1>

      <div className="flex gap-2 mb-6">
        <Button variant={tab === 'profile' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('profile')}>Profile</Button>
        <Button variant={tab === '2fa' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('2fa')}>2FA</Button>
        <Button variant={tab === 'sessions' ? 'primary' : 'secondary'} size="sm" onClick={() => setTab('sessions')}>Sessions</Button>
      </div>

      {tab === 'profile' && (
        <Card title="Account Information">
          <div className="space-y-3">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Email</span><span className="text-text-primary">{user?.email}</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Role</span><Badge>{user?.role || 'user'}</Badge></div>
          </div>
        </Card>
      )}

      {tab === '2fa' && (
        <Card title="Two-Factor Authentication">
          {totpLoading ? <Loading className="h-32" /> : (
            <div className="space-y-4">
              {totpSetup && (
                <div>
                  <p className="text-sm text-text-secondary mb-2">Scan this QR code with your authenticator app, or enter the secret manually:</p>
                  <div className="bg-bg-elevated rounded-lg p-3 font-mono text-xs break-all text-text-primary">{totpSetup.secret}</div>
                  {totpSetup.url && <p className="text-xs text-text-muted mt-2">URL: {totpSetup.url}</p>}
                </div>
              )}
              <div className="flex gap-3">
                <Input placeholder="Enter 6-digit code" value={totpCode} onChange={(e) => setTotpCode(e.target.value)} />
                <Button onClick={() => { if (totpCode && totpSetup?.secret) enableTotpMutation.mutate({ secret: totpSetup.secret, code: totpCode }) }} loading={enableTotpMutation.isPending}>Verify & Enable</Button>
              </div>
              {enableTotpMutation.isError && <p className="text-sm text-danger">{(enableTotpMutation.error as Error).message}</p>}
            </div>
          )}
        </Card>
      )}

      {tab === 'sessions' && (
        <Card title="Active Sessions">
          <div className="bg-bg-elevated rounded-lg p-4">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm font-medium text-text-primary">Current Session</p>
                <p className="text-xs text-text-secondary">Logged in from this device</p>
              </div>
              <Badge variant="success">Active</Badge>
            </div>
          </div>
        </Card>
      )}
    </div>
  )
}
