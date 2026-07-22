import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { useAuthStore } from '@shared'
import { LoginPage } from './pages/Login'
import { BootstrapPage } from './pages/Bootstrap'
import { Layout } from './components/Layout'
import { RequireRole } from './components/RequireRole'
import { Dashboard } from './pages/Dashboard'
import { TenantsPage } from './pages/Tenants'
import { DomainsPage } from './pages/Domains'
import { UsersPage } from './pages/Users'
import { LicensePage } from './pages/License'
import { FeaturesPage } from './pages/Features'
import { ProvisioningJobsPage } from './pages/ProvisioningJobs'
import { AuditLogsPage } from './pages/AuditLogs'
import { DNSWizardPage } from './pages/DNSWizard'
import { MailQueuePage } from './pages/MailQueue'
import { FirewallPage } from './pages/Firewall'
import { GeoBlockingPage } from './pages/GeoBlocking'
import { GuardianPage } from './pages/Guardian'
import { AutoHealPage } from './pages/AutoHeal'
import { BackupPage } from './pages/Backup'
import { MigrationPage } from './pages/Migration'
import { WebhooksPage } from './pages/Webhooks'
import { APIKeysPage } from './pages/APIKeys'
import { DistributionListsPage } from './pages/DistributionLists'
import { ResourcesPage } from './pages/Resources'
import { PublicFoldersPage } from './pages/PublicFolders'
import { RoutingRulesPage } from './pages/RoutingRules'
import { DLPPage } from './pages/DLP'
import { SLAPage } from './pages/SLA'
import { LDAPPage } from './pages/LDAP'
import { SSOPage } from './pages/SSO'
import { CompliancePage } from './pages/Compliance'
import { IntelligencePage } from './pages/Intelligence'
import { UpdatesPage } from './pages/Updates'
import { AntiSpamPage } from './pages/AntiSpam'
import { LogViewerPage } from './pages/LogViewer'
import { EmailRoutingPage } from './pages/EmailRouting'
import { MaintenancePage } from './pages/Maintenance'

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
        <Route path="/bootstrap" element={<BootstrapPage />} />
        <Route path="/403" element={
          <div className="flex items-center justify-center h-screen text-center p-8">
            <div>
              <h2 className="text-4xl font-bold text-danger mb-2">403 Forbidden</h2>
              <p className="text-text-secondary">You do not have permission to access this resource.</p>
            </div>
          </div>
        } />
        <Route
          path="/*"
          element={
            <ProtectedRoute>
              <Layout>
                <Routes>
                  {/* Shared routes */}
                  <Route path="/" element={<Dashboard />} />

                  {/* Super Admin only */}
                  <Route path="/tenants" element={<RequireRole role="super_admin"><TenantsPage /></RequireRole>} />
                  <Route path="/features" element={<RequireRole role="super_admin"><FeaturesPage /></RequireRole>} />
                  <Route path="/maintenance" element={<RequireRole role="super_admin"><MaintenancePage /></RequireRole>} />

                  {/* Tenant Admin + Super Admin */}
                  <Route path="/users" element={<RequireRole role={['tenant_admin', 'super_admin']}><UsersPage /></RequireRole>} />
                  <Route path="/domains" element={<RequireRole role={['tenant_admin', 'super_admin']}><DomainsPage /></RequireRole>} />
                  <Route path="/license" element={<RequireRole role={['tenant_admin', 'super_admin']}><LicensePage /></RequireRole>} />

                  {/* Operational — both roles */}
                  <Route path="/mail-queue" element={<RequireRole role={['tenant_admin', 'super_admin']}><MailQueuePage /></RequireRole>} />
                  <Route path="/firewall" element={<RequireRole role={['tenant_admin', 'super_admin']}><FirewallPage /></RequireRole>} />
                  <Route path="/geo-blocking" element={<RequireRole role={['tenant_admin', 'super_admin']}><GeoBlockingPage /></RequireRole>} />
                  <Route path="/anti-spam" element={<RequireRole role={['tenant_admin', 'super_admin']}><AntiSpamPage /></RequireRole>} />
                  <Route path="/guardian" element={<RequireRole role={['tenant_admin', 'super_admin']}><GuardianPage /></RequireRole>} />
                  <Route path="/auto-heal" element={<RequireRole role={['tenant_admin', 'super_admin']}><AutoHealPage /></RequireRole>} />
                  <Route path="/dns" element={<RequireRole role={['tenant_admin', 'super_admin']}><DNSWizardPage /></RequireRole>} />
                  <Route path="/backup" element={<RequireRole role={['tenant_admin', 'super_admin']}><BackupPage /></RequireRole>} />
                  <Route path="/audit-logs" element={<RequireRole role={['tenant_admin', 'super_admin']}><AuditLogsPage /></RequireRole>} />
                  <Route path="/webhooks" element={<RequireRole role={['tenant_admin', 'super_admin']}><WebhooksPage /></RequireRole>} />
                  <Route path="/api-keys" element={<RequireRole role={['tenant_admin', 'super_admin']}><APIKeysPage /></RequireRole>} />
                  <Route path="/logs" element={<RequireRole role={['tenant_admin', 'super_admin']}><LogViewerPage /></RequireRole>} />
                  <Route path="/updates" element={<RequireRole role={['tenant_admin', 'super_admin']}><UpdatesPage /></RequireRole>} />
                  <Route path="/provisioning" element={<ProvisioningJobsPage />} />
                  <Route path="/distribution-lists" element={<DistributionListsPage />} />
                  <Route path="/resources" element={<ResourcesPage />} />
                  <Route path="/public-folders" element={<PublicFoldersPage />} />
                  <Route path="/routing-rules" element={<RoutingRulesPage />} />
                  <Route path="/dlp" element={<DLPPage />} />
                  <Route path="/sla" element={<SLAPage />} />
                  <Route path="/ldap" element={<LDAPPage />} />
                  <Route path="/sso" element={<SSOPage />} />
                  <Route path="/compliance" element={<CompliancePage />} />
                  <Route path="/intelligence" element={<IntelligencePage />} />
                  <Route path="/migration" element={<MigrationPage />} />
                  <Route path="/email-routing" element={<EmailRoutingPage />} />
                </Routes>
              </Layout>
            </ProtectedRoute>
          }
        />
      </Routes>
    </BrowserRouter>
  )
}
