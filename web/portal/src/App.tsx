import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { useAuthStore } from '@shared'
import { LoginPage } from './pages/Login'
import { PortalLayout } from './components/Layout'
import { DashboardPage } from './pages/Dashboard'
import { DomainsPage } from './pages/Domains'
import { LicensePage } from './pages/License'
import { BillingPage } from './pages/Billing'
import { SupportPage } from './pages/Support'
import { DownloadsPage } from './pages/Downloads'
import { ChangelogPage } from './pages/Changelog'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const token = useAuthStore((s) => s.token)
  if (!token) return <Navigate to="/login" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/*" element={<ProtectedRoute>
          <PortalLayout>
            <Routes>
              <Route path="/" element={<DashboardPage />} />
              <Route path="/domains" element={<DomainsPage />} />
              <Route path="/license" element={<LicensePage />} />
              <Route path="/billing" element={<BillingPage />} />
              <Route path="/support" element={<SupportPage />} />
              <Route path="/downloads" element={<DownloadsPage />} />
              <Route path="/changelog" element={<ChangelogPage />} />
            </Routes>
          </PortalLayout>
        </ProtectedRoute>} />
      </Routes>
    </BrowserRouter>
  )
}
