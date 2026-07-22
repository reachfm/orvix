import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Table, TableHead, TableBody, TableRow, TableHeader, TableCell, Badge, Loading } from '@shared'

export function DNSWizardPage() {
  const [domain, setDomain] = useState('')
  const [checking, setChecking] = useState(false)
  const [results, setResults] = useState<any>(null)

  const { data: domains, isLoading } = useQuery({
    queryKey: ['domains'],
    queryFn: () => apiRequest<any>('/admin/domains'),
  })

  const checkDNS = async () => {
    if (!domain) return
    setChecking(true)
    try {
      const res = await apiRequest<any>(`/dns/check?domain=${encodeURIComponent(domain)}`)
      setResults(res.records)
    } catch (err) {
      setResults({ mx: false, spf: false, dkim: false, dmarc: false })
    }
    setChecking(false)
  }

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">DNS Wizard</h1>

      <Card title="Check DNS Records" className="mb-6">
        <div className="flex gap-3">
          <Input placeholder="example.com" value={domain} onChange={(e) => setDomain(e.target.value)} />
          <Button onClick={checkDNS} loading={checking}>Check</Button>
        </div>
        {results && (
          <div className="mt-4 space-y-2">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">MX Record</span><Badge variant={results.mx ? 'success' : 'danger'}>{results.mx ? 'Found' : 'Missing'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">SPF Record</span><Badge variant={results.spf ? 'success' : 'danger'}>{results.spf ? 'Found' : 'Missing'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">DKIM Record</span><Badge variant={results.dkim ? 'success' : 'danger'}>{results.dkim ? 'Found' : 'Missing'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">DMARC Record</span><Badge variant={results.dmarc ? 'success' : 'danger'}>{results.dmarc ? 'Found' : 'Missing'}</Badge></div>
          </div>
        )}
      </Card>

      <Card title="Managed Domains">
        {isLoading ? <Loading className="h-32" /> : (
          <Table>
            <TableHead>
              <TableRow>
                <TableHeader>Domain</TableHeader>
                <TableHeader>DKIM Selector</TableHeader>
                <TableHeader>SPF Record</TableHeader>
                <TableHeader>DMARC</TableHeader>
              </TableRow>
            </TableHead>
            <TableBody>
              {(domains?.domains || []).map((d: any) => (
                <TableRow key={d.id}>
                  <TableCell className="font-medium">{d.name}</TableCell>
                  <TableCell className="font-mono text-xs">{d.dkim_selector || '-'}</TableCell>
                  <TableCell className="text-text-secondary text-xs max-w-[200px] truncate">{d.spf_record || '-'}</TableCell>
                  <TableCell className="text-text-secondary">{d.dmarc_policy || '-'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </Card>
    </div>
  )
}
