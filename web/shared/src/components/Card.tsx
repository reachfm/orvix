import React from 'react'
import { cn } from '../utils'

interface CardProps {
  children: React.ReactNode
  className?: string
  title?: string
  description?: string
  action?: React.ReactNode
}

export function Card({ children, className, title, description, action }: CardProps) {
  return (
    <div className={cn('rounded-xl border border-border bg-bg-surface p-6', className)}>
      {(title || description || action) && (
        <div className="mb-4 flex items-start justify-between">
          <div>
            {title && <h3 className="text-lg font-semibold text-text-primary">{title}</h3>}
            {description && <p className="mt-1 text-sm text-text-secondary">{description}</p>}
          </div>
          {action && <div>{action}</div>}
        </div>
      )}
      {children}
    </div>
  )
}
