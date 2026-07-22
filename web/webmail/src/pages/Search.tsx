import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { apiRequest, Card, Button, Input, Loading } from '@shared'

export function SearchPage() {
  const [query, setQuery] = useState('')

  const { data, isLoading } = useQuery({
    queryKey: ['search', query],
    queryFn: () => apiRequest<any>('/users'),
    enabled: query.length > 0,
  })

  return (
    <div className="p-6">
      <div className="flex items-center gap-3 mb-6">
        <Input placeholder="Search emails, contacts, and more..." value={query} onChange={(e) => setQuery(e.target.value)} className="flex-1" />
        {query && <Button variant="secondary" onClick={() => setQuery('')}>Clear</Button>}
      </div>
      {query && (
        <Card>
          {isLoading ? <Loading className="h-32" /> : (
            <div className="space-y-2">
              {(data?.users || []).filter((u: any) => u.email?.includes(query) || u.username?.includes(query)).map((u: any, i: number) => (
                <div key={i} className="p-3 bg-bg-elevated rounded-lg text-sm text-text-primary">
                  {u.email} {u.username && `(${u.username})`}
                </div>
              ))}
              {data?.users?.filter((u: any) => u.email?.includes(query)).length === 0 && (
                <p className="text-text-secondary text-center py-4">No results found for "{query}"</p>
              )}
            </div>
          )}
        </Card>
      )}
    </div>
  )
}
