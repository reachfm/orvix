import React from 'react'
import { cn } from '../utils'

interface TableProps {
  children: React.ReactNode
  className?: string
}

export function Table({ children, className }: TableProps) {
  return (
    <div className={cn('overflow-x-auto rounded-lg border border-border', className)}>
      <table className="min-w-full divide-y divide-border">{children}</table>
    </div>
  )
}

export function TableHead({ children, className }: { children: React.ReactNode; className?: string }) {
  return <thead className={cn('bg-bg-elevated', className)}>{children}</thead>
}

export function TableBody({ children, className }: { children: React.ReactNode; className?: string }) {
  return <tbody className={cn('divide-y divide-border bg-bg-surface', className)}>{children}</tbody>
}

export function TableRow({ children, className }: { children: React.ReactNode; className?: string }) {
  return <tr className={cn('transition-colors hover:bg-bg-subtle', className)}>{children}</tr>
}

export function TableHeader({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <th className={cn('px-4 py-3 text-left text-xs font-semibold uppercase tracking-wider text-text-secondary', className)}>
      {children}
    </th>
  )
}

export function TableCell({ children, className }: { children: React.ReactNode; className?: string }) {
  return <td className={cn('whitespace-nowrap px-4 py-3 text-sm text-text-primary', className)}>{children}</td>
}
