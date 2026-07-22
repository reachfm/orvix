import { Card, Badge } from '@shared'

const changelog = [
  { version: '0.1.0', date: '2026-06-05', channel: 'nightly', changes: ['Initial MVP release', 'Auth system with JWT + TOTP 2FA', 'Tenant and domain management', 'DNS wizard with DKIM/SPF/DMARC', 'Mail firewall with rules engine', 'Guardian AI threat analysis', 'Auto-heal system', 'Compliance center', 'Webhook system', 'DLP policies', 'LDAP/AD sync config', 'SSO configuration', 'Admin console UI', 'Webmail UI', 'Customer portal UI'] },
]

export function ChangelogPage() {
  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Changelog</h1>
      <Card className="mb-4">
        <p className="text-text-secondary text-sm mb-4">Update channel: <strong>nightly</strong> — Updates are checked from <strong>updates.orvix.email</strong>.</p>
        <div className="flex gap-2">
          {['stable', 'beta', 'early-access', 'nightly'].map((ch) => (
            <Badge key={ch} variant={ch === 'nightly' ? 'info' : 'default'}>{ch}</Badge>
          ))}
        </div>
      </Card>

      <div className="space-y-4">
        {changelog.map((release) => (
          <Card key={release.version} title={`v${release.version}`} description={`${release.date} · ${release.channel}`}>
            <ul className="space-y-1 mt-2">
              {release.changes.map((change, i) => (
                <li key={i} className="flex items-start gap-2 text-sm text-text-primary">
                  <span className="text-success mt-0.5">✓</span> {change}
                </li>
              ))}
            </ul>
          </Card>
        ))}
      </div>
    </div>
  )
}
