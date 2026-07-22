import { useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Badge, Modal, Loading } from '@shared'

export function CompliancePage() {
  const [showLegalHold, setShowLegalHold] = useState(false)
  const [showEDiscovery, setShowEDiscovery] = useState(false)
  const [lhForm, setLHForm] = useState({ user_id: 1, reason: '', case_id: '' })
  const [edForm, setEDForm] = useState({ query: '', date_from: '', date_to: '' })

  const { data, isLoading } = useQuery({ queryKey: ['compliance'], queryFn: () => apiRequest<any>('/compliance/status') })
  const legalMutation = useMutation({ mutationFn: (body: any) => apiRequest('/compliance/legal-hold', { method: 'POST', body }), onSuccess: () => setShowLegalHold(false) })
  const edMutation = useMutation({ mutationFn: (body: any) => apiRequest('/compliance/ediscovery', { method: 'POST', body }), onSuccess: () => setShowEDiscovery(false) })

  if (isLoading) return <Loading className="h-64" />

  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Compliance Center</h1>

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-6">
        <Card title="GDPR" description="General Data Protection Regulation">
          <div className="space-y-2 mt-2">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Status</span><Badge variant={data?.gdpr?.enabled ? 'success' : 'danger'}>{data?.gdpr?.enabled ? 'Compliant' : 'Not Configured'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Data Retention</span><span className="text-text-primary">{data?.gdpr?.data_retention_days || 365} days</span></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Right to Erasure</span><span className={data?.gdpr?.right_to_erasure ? 'text-success' : 'text-danger'}>{data?.gdpr?.right_to_erasure ? 'Enabled' : 'Disabled'}</span></div>
          </div>
        </Card>
        <Card title="HIPAA" description="Health Insurance Portability and Accountability Act">
          <div className="space-y-2 mt-2">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Status</span><Badge variant={data?.hipaa?.enabled ? 'success' : 'default'}>{data?.hipaa?.enabled ? 'Compliant' : 'Not Configured'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Audit Logging</span><span className={data?.hipaa?.audit_logging ? 'text-success' : 'text-danger'}>{data?.hipaa?.audit_logging ? 'Enabled' : 'Disabled'}</span></div>
          </div>
        </Card>
        <Card title="SOX" description="Sarbanes-Oxley Act">
          <div className="space-y-2 mt-2">
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Status</span><Badge variant={data?.sox?.enabled ? 'success' : 'default'}>{data?.sox?.enabled ? 'Compliant' : 'Not Configured'}</Badge></div>
            <div className="flex justify-between text-sm"><span className="text-text-secondary">Records Retention</span><span className={data?.sox?.financial_records_retention ? 'text-success' : 'text-danger'}>{data?.sox?.financial_records_retention ? 'Enabled' : 'Disabled'}</span></div>
          </div>
        </Card>
      </div>

      <div className="flex gap-4 mb-6">
        <Button onClick={() => setShowLegalHold(true)}>Create Legal Hold</Button>
        <Button onClick={() => setShowEDiscovery(true)} variant="secondary">eDiscovery Search</Button>
      </div>

      <Modal open={showLegalHold} onClose={() => setShowLegalHold(false)} title="Create Legal Hold">
        <form onSubmit={(e) => { e.preventDefault(); legalMutation.mutate(lhForm) }} className="space-y-4">
          <Input label="User ID" type="number" value={lhForm.user_id} onChange={(e) => setLHForm({ ...lhForm, user_id: parseInt(e.target.value) })} required />
          <Input label="Reason" value={lhForm.reason} onChange={(e) => setLHForm({ ...lhForm, reason: e.target.value })} required />
          <Input label="Case ID" value={lhForm.case_id} onChange={(e) => setLHForm({ ...lhForm, case_id: e.target.value })} />
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowLegalHold(false)}>Cancel</Button>
            <Button type="submit" loading={legalMutation.isPending}>Create</Button>
          </div>
        </form>
      </Modal>

      <Modal open={showEDiscovery} onClose={() => setShowEDiscovery(false)} title="eDiscovery Search">
        <form onSubmit={(e) => { e.preventDefault(); edMutation.mutate(edForm) }} className="space-y-4">
          <Input label="Search Query" value={edForm.query} onChange={(e) => setEDForm({ ...edForm, query: e.target.value })} required />
          <Input label="Date From" type="date" value={edForm.date_from} onChange={(e) => setEDForm({ ...edForm, date_from: e.target.value })} />
          <Input label="Date To" type="date" value={edForm.date_to} onChange={(e) => setEDForm({ ...edForm, date_to: e.target.value })} />
          <div className="flex gap-3 justify-end">
            <Button variant="secondary" onClick={() => setShowEDiscovery(false)}>Cancel</Button>
            <Button type="submit" loading={edMutation.isPending}>Search</Button>
          </div>
        </form>
      </Modal>
    </div>
  )
}
