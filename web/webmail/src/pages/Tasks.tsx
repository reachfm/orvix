import { useState } from 'react'
import { Card, Button, Input, EmptyState } from '@shared'

export function TasksPage() {
  const [tasks, setTasks] = useState<Array<{ id: number; title: string; done: boolean; due: string }>>([])
  const [newTask, setNewTask] = useState('')
  const [dueDate, setDueDate] = useState('')

  const addTask = () => {
    if (!newTask) return
    setTasks([...tasks, { id: Date.now(), title: newTask, done: false, due: dueDate }])
    setNewTask('')
    setDueDate('')
  }

  return (
    <div className="p-6">
      <h1 className="text-2xl font-bold text-text-primary mb-6">Tasks</h1>
      <Card className="mb-6">
        <div className="flex gap-3">
          <Input placeholder="Add a task..." value={newTask} onChange={(e) => setNewTask(e.target.value)} className="flex-1" />
          <Input type="date" value={dueDate} onChange={(e) => setDueDate(e.target.value)} className="w-40" />
          <Button onClick={addTask}>Add</Button>
        </div>
      </Card>
      {tasks.length === 0 ? (
        <EmptyState title="No tasks" description="Add tasks to track your to-dos." />
      ) : (
        <div className="space-y-2">
          {tasks.map((t) => (
            <Card key={t.id} className="flex items-center gap-3 py-3">
              <input type="checkbox" checked={t.done} onChange={() => setTasks(tasks.map(tt => tt.id === t.id ? { ...tt, done: !tt.done } : tt))} className="rounded border-border" />
              <span className={`flex-1 text-sm ${t.done ? 'line-through text-text-muted' : 'text-text-primary'}`}>{t.title}</span>
              {t.due && <span className="text-xs text-text-secondary">{t.due}</span>}
              <Button variant="ghost" size="sm" onClick={() => setTasks(tasks.filter(tt => tt.id !== t.id))}>✕</Button>
            </Card>
          ))}
        </div>
      )}
    </div>
  )
}
