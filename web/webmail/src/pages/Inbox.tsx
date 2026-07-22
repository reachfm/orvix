import { useState, useEffect, useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery, useMutation } from '@tanstack/react-query'
import { useAuthStore, apiRequest, Button, Input, Modal, Loading, EmptyState, timeAgo, getInitials, avatarColor } from '@shared'
import { useEditor, EditorContent } from '@tiptap/react'
import StarterKit from '@tiptap/starter-kit'
import Placeholder from '@tiptap/extension-placeholder'
import Underline from '@tiptap/extension-underline'

const folders = ['Inbox', 'Sent', 'Drafts', 'Spam', 'Trash', 'Archive']

function ComposeModal({ open, onClose, replyTo, forwardContent }: {
  open: boolean; onClose: () => void
  replyTo?: { email: string; subject: string } | null
  forwardContent?: string | null
}) {
  const [to, setTo] = useState(replyTo?.email || '')
  const [subject, setSubject] = useState(replyTo ? `Re: ${replyTo.subject}` : '')
  const [files, setFiles] = useState<File[]>([])
  const [sent, setSent] = useState(false)
  const [showUndo, setShowUndo] = useState(false)

  useEffect(() => {
    if (!open) { setTo(''); setSubject(''); setFiles([]); setSent(false); setShowUndo(false) }
  }, [open])

  // Auto-save draft every 30 seconds
  useEffect(() => {
    if (!open || !subject) return
    const interval = setInterval(() => {
      const html = editor?.getHTML() || ''
      localStorage.setItem('orvixem-draft', JSON.stringify({ to, subject, body: html }))
    }, 30000)
    return () => clearInterval(interval)
  }, [open, to, subject])

  // Restore draft on mount
  useEffect(() => {
    if (!open) return
    const draft = localStorage.getItem('orvixem-draft')
    if (draft) {
      try {
        const d = JSON.parse(draft)
        if (d.to) setTo(d.to)
        if (d.subject) setSubject(d.subject)
      } catch {}
    }
  }, [open])

  const editor = useEditor({
    extensions: [StarterKit, Underline, Placeholder.configure({ placeholder: 'Write your message...' })],
    editorProps: { attributes: { class: 'prose prose-sm max-w-none focus:outline-none min-h-[200px] px-4 py-2 text-text-primary' } },
    content: forwardContent || '',
  })

  const sendMutation = useMutation({
    mutationFn: (body: any) => apiRequest('/compose/send', { method: 'POST', body }),
    onSuccess: () => {
      setSent(true)
      setShowUndo(true)
      localStorage.removeItem('orvixem-draft')
      setTimeout(() => { setShowUndo(false); onClose() }, 30000)
    },
  })

  const handleSend = useCallback(() => {
    const html = editor?.getHTML() || ''
    sendMutation.mutate({ to, subject, body: html, from: '' })
  }, [to, subject, editor, sendMutation])

  // Keyboard shortcut: Ctrl+Enter to send
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
        e.preventDefault()
        if (to && subject) handleSend()
      }
    }
    if (open) window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [open, to, subject, handleSend])

  return (
    <Modal open={open} onClose={onClose} title="New Message" className="max-w-2xl">
      {showUndo ? (
        <div className="text-center py-8">
          <p className="text-lg font-semibold text-text-primary mb-2">Message sent!</p>
          <p className="text-text-secondary text-sm mb-4">You can undo this action within 30 seconds.</p>
          <Button variant="secondary" onClick={() => { setShowUndo(false); setSent(false); onClose() }}>Undo Send</Button>
        </div>
      ) : (
      <div className="space-y-4">
        <Input label="To" type="email" value={to} onChange={(e) => setTo(e.target.value)} required />
        <Input label="Subject" value={subject} onChange={(e) => setSubject(e.target.value)} required />
        <div>
          <label className="mb-1.5 block text-sm font-medium text-text-secondary">Message <span className="text-text-muted">(Ctrl+Enter to send)</span></label>
          <div className="rounded-lg border border-border bg-bg-elevated overflow-hidden">
            <div className="flex items-center gap-1 border-b border-border px-3 py-1.5">
              {[
                { cmd: 'toggleBold', label: 'B', html: '<strong>B</strong>', active: 'bold' as const },
                { cmd: 'toggleItalic', label: 'I', html: '<em>I</em>', active: 'italic' as const },
                { cmd: 'toggleUnderline', label: 'U', html: '<u>U</u>', active: 'underline' as const },
              ].map((btn) => (
                <button key={btn.cmd} type="button" onClick={() => (editor?.chain().focus() as any)[btn.cmd]().run()}
                  className={`p-1 rounded hover:bg-bg-subtle ${editor?.isActive(btn.active) ? 'bg-accent/20 text-accent' : 'text-text-secondary'}`}
                  dangerouslySetInnerHTML={{ __html: btn.html }} />
              ))}
              <span className="text-text-muted mx-1">|</span>
              <button type="button" onClick={() => editor?.chain().focus().toggleBulletList().run()}
                className={`p-1 rounded hover:bg-bg-subtle ${editor?.isActive('bulletList') ? 'bg-accent/20 text-accent' : 'text-text-secondary'}`}>≡</button>
            </div>
            <EditorContent editor={editor} />
          </div>
        </div>
        <div>
          <label className="mb-1.5 block text-sm font-medium text-text-secondary">Attachments</label>
          <input type="file" multiple onChange={(e) => setFiles(Array.from(e.target.files || []))}
            className="w-full text-sm text-text-secondary file:mr-3 file:py-1.5 file:px-3 file:rounded-lg file:border-0 file:bg-bg-elevated file:text-sm file:text-text-primary hover:file:bg-bg-subtle" />
          {files.length > 0 && <p className="text-xs text-text-muted mt-1">{files.length} file(s) selected</p>}
        </div>
        {sendMutation.isError && <p className="text-sm text-danger">{(sendMutation.error as Error).message}</p>}
        {sendMutation.isSuccess && <p className="text-sm text-success">Message queued for delivery.</p>}
        <div className="flex gap-3 justify-end">
          <Button variant="secondary" onClick={onClose}>Discard</Button>
          <Button onClick={handleSend} loading={sendMutation.isPending}>Send</Button>
        </div>
      </div>
      )}
    </Modal>
  )
}

// Webmail keyboard shortcuts
function useKeyboardShortcuts(activeFolder: string, selectedEmail: any, onReply: () => void, onForward: () => void) {
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      switch (e.key) {
        case 'r': case 'R': e.preventDefault(); if (selectedEmail) onReply(); break
        case 'f': case 'F': e.preventDefault(); if (selectedEmail) onForward(); break
        case 'j': case 'J': e.preventDefault(); break
        case 'k': case 'K': e.preventDefault(); break
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [selectedEmail, onReply, onForward])
}

export function InboxLayout() {
  const navigate = useNavigate()
  const { user, clearAuth } = useAuthStore()
  const [activeFolder, setActiveFolder] = useState('Inbox')
  const [showCompose, setShowCompose] = useState(false)
  const [composeMode, setComposeMode] = useState<{ replyTo?: any; forwardContent?: string }>({})
  const [selectedEmail, setSelectedEmail] = useState<any>(null)

  const { data: messages } = useQuery({
    queryKey: ['messages', activeFolder],
    queryFn: () => apiRequest<any>(`/messages?folder=${activeFolder.toLowerCase()}`),
  })

  const handleLogout = () => { clearAuth(); navigate('/login') }
  const emailList = messages?.users || []
  const chars = getInitials(user?.email || 'U')
  const color = avatarColor(user?.email || 'U')

  const openReply = () => {
    setComposeMode({ replyTo: { email: selectedEmail.email, subject: selectedEmail.email || 'Re: Email' } })
    setShowCompose(true)
  }

  const openForward = () => {
    setComposeMode({ forwardContent: '<p>--- Forwarded message ---</p><p>Original content here.</p>' })
    setShowCompose(true)
  }

  useKeyboardShortcuts(activeFolder, selectedEmail, openReply, openForward)

  return (
    <div className="flex h-screen bg-bg-base">
      <aside className="w-60 border-r border-border bg-bg-surface flex flex-col">
        <div className="p-4 border-b border-border">
          <h1 className="text-lg font-bold text-accent">OrvixEM Mail</h1>
          <p className="text-xs text-text-muted">mail.orvix.email</p>
        </div>
        <div className="p-3">
          <Button className="w-full" onClick={() => { setComposeMode({}); setShowCompose(true) }}>✏️ Compose</Button>
        </div>
        <nav className="flex-1 p-2 space-y-1">
          {folders.map((f) => (
            <button key={f} onClick={() => setActiveFolder(f)}
              className={`w-full text-left rounded-lg px-3 py-2 text-sm transition-colors ${activeFolder === f ? 'bg-accent text-white' : 'text-text-secondary hover:bg-bg-subtle hover:text-text-primary'}`}>
              📁 {f}
            </button>
          ))}
        </nav>
        <div className="p-3 border-t border-border">
          <button onClick={() => navigate('/settings')} className="w-full text-left rounded-lg px-3 py-2 text-sm text-text-secondary hover:bg-bg-subtle hover:text-text-primary">⚙️ Settings</button>
          <button onClick={() => navigate('/tasks')} className="w-full text-left rounded-lg px-3 py-2 text-sm text-text-secondary hover:bg-bg-subtle hover:text-text-primary">✅ Tasks</button>
        </div>
        <div className="p-4 border-t border-border flex items-center gap-3">
          <div className="w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold text-white" style={{ background: color }}>{chars}</div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium text-text-primary truncate">{user?.email}</p>
          </div>
          <button onClick={handleLogout} className="text-text-secondary hover:text-text-primary" title="Logout">🚪</button>
        </div>
      </aside>

      <div className="flex-1 flex flex-col">
        <div className="flex items-center justify-between p-4 border-b border-border">
          <h2 className="text-lg font-semibold text-text-primary">{activeFolder}</h2>
          <button onClick={() => navigate('/search')} className="text-text-secondary hover:text-text-primary">🔍 Search</button>
        </div>

        <div className="flex-1 flex">
          <div className="w-[400px] border-r border-border overflow-y-auto">
            {emailList.length === 0 ? (
              <EmptyState title={`${activeFolder} is empty`} description="No messages in this folder." className="py-12" />
            ) : (
              emailList.slice(0, 20).map((msg: any, i: number) => (
                <div key={i} onClick={() => setSelectedEmail(msg)}
                  className={`p-4 border-b border-border cursor-pointer transition-colors hover:bg-bg-subtle ${selectedEmail?.id === msg.id ? 'bg-bg-subtle' : ''}`}>
                  <div className="flex items-center gap-3">
                    <div className="w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold text-white" style={{ background: avatarColor(msg.email || 'U') }}>
                      {getInitials(msg.email || 'U')}
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm font-medium text-text-primary truncate">{msg.email || 'Unknown'}</p>
                      <p className="text-xs text-text-secondary truncate mt-0.5">{msg.username ? `Subject: Welcome to OrvixEM` : 'No subject'}</p>
                      <p className="text-xs text-text-muted mt-0.5">{timeAgo(new Date())}</p>
                    </div>
                  </div>
                </div>
              ))
            )}
          </div>

          <div className="flex-1 overflow-y-auto p-6">
            {selectedEmail ? (
              <div>
                <div className="flex items-center gap-2 mb-4">
                  <h2 className="text-xl font-semibold text-text-primary flex-1">Welcome to OrvixEM</h2>
                  <div className="flex gap-2">
                    <Button size="sm" variant="secondary" onClick={openReply}>↩ Reply</Button>
                    <Button size="sm" variant="secondary" onClick={openForward}>↪ Forward</Button>
                  </div>
                </div>
                <div className="flex items-center gap-3 mb-4">
                  <div className="w-10 h-10 rounded-full flex items-center justify-center text-sm font-bold text-white" style={{ background: avatarColor(selectedEmail.email || 'U') }}>
                    {getInitials(selectedEmail.email || 'U')}
                  </div>
                  <div>
                    <p className="text-sm font-medium text-text-primary">{selectedEmail.email}</p>
                    <p className="text-xs text-text-secondary">{new Date().toLocaleString()}</p>
                  </div>
                </div>
                {/* HTML email in sandboxed iframe */}
                <div className="rounded-lg border border-border overflow-hidden">
                  <iframe
                    title="Email content"
                    sandbox="allow-same-origin"
                    className="w-full h-[400px] bg-white"
                    srcDoc={`<!DOCTYPE html><html><head><meta charset="utf-8"><style>body{font-family:sans-serif;padding:16px;color:#333;line-height:1.6}</style></head><body>
                      <p>Welcome to OrvixEM!</p>
                      <p>Your email account is ready. You can start sending and receiving emails.</p>
                      <p>Features available:</p>
                      <ul>
                        <li>Webmail access at mail.orvix.email</li>
                        <li>Calendar and contacts sync</li>
                        <li>Smart Compose AI assistance</li>
                        <li>2FA security</li>
                      </ul>
                    </body></html>`}
                  />
                </div>
              </div>
            ) : (
              <div className="flex items-center justify-center h-full text-text-muted">
                <p>Select a message to read</p>
              </div>
            )}
          </div>
        </div>
      </div>

      <ComposeModal
        open={showCompose}
        onClose={() => setShowCompose(false)}
        replyTo={composeMode.replyTo || null}
        forwardContent={composeMode.forwardContent || null}
      />
    </div>
  )
}
