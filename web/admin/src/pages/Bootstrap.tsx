import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { apiRequest, Button, Input, Card } from '@shared'

export function BootstrapPage() {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState(false)

  const handleBootstrap = async (e: React.FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      await apiRequest('/admin/bootstrap', {
        method: 'POST',
        body: { email, password },
      })
      setSuccess(true)
      setTimeout(() => navigate('/login'), 2000)
    } catch (err: any) {
      setError(err.message || 'Bootstrap failed')
    } finally {
      setLoading(false)
    }
  }

  if (success) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-bg-base p-4">
        <Card className="w-full max-w-md text-center">
          <div className="text-4xl mb-4">✅</div>
          <h2 className="text-xl font-bold text-text-primary">Admin Created</h2>
          <p className="text-text-secondary mt-2">Redirecting to login...</p>
        </Card>
      </div>
    )
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-bg-base p-4">
      <Card className="w-full max-w-md">
        <div className="text-center mb-6">
          <h1 className="text-2xl font-bold text-accent">Bootstrap Admin</h1>
          <p className="text-text-secondary mt-2">Create your first admin account</p>
        </div>

        <form onSubmit={handleBootstrap} className="space-y-4">
          <Input
            label="Admin Email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="admin@orvix.email"
            required
          />
          <Input
            label="Password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="••••••••"
            required
          />

          {error && (
            <div className="rounded-lg bg-danger/10 border border-danger/20 p-3 text-sm text-danger">
              {error}
            </div>
          )}

          <Button type="submit" className="w-full" loading={loading}>
            Create Admin Account
          </Button>

          <div className="text-center">
            <button
              type="button"
              onClick={() => navigate('/login')}
              className="text-sm text-accent hover:underline"
            >
              Already have an account? Sign in
            </button>
          </div>
        </form>
      </Card>
    </div>
  )
}
