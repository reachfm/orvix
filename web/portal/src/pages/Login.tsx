import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useAuthStore, apiRequest, Button, Input, Card } from '@shared'

export function LoginPage() {
  const navigate = useNavigate()
  const { setAuth } = useAuthStore()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  const handleLogin = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      const data = await apiRequest<any>('/auth/login', { method: 'POST', body: { email, password } })
      if (data.totp_required) { setError('2FA required'); setLoading(false); return }
      setAuth(data.access_token, data.refresh_token, { id: data.user_id, email: data.email, role: data.role })
      navigate('/')
    } catch (err: any) {
      setError(err.message || 'Login failed')
    } finally { setLoading(false) }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg-base p-4">
      <Card className="w-full max-w-md">
        <div className="text-center mb-6">
          <h1 className="text-2xl font-bold text-accent">OrvixEM Portal</h1>
          <p className="text-text-secondary mt-2">portal.orvix.email</p>
        </div>
        <form onSubmit={handleLogin} className="space-y-4">
          <Input label="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="user@orvix.email" required />
          <Input label="Password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="••••••••" required />
          {error && <div className="rounded-lg bg-danger/10 border border-danger/20 p-3 text-sm text-danger">{error}</div>}
          <Button type="submit" className="w-full" loading={loading}>Sign In</Button>
        </form>
      </Card>
    </div>
  )
}
