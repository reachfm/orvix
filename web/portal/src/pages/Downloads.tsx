import { Card } from '@shared'

export function DownloadsPage() {
  return (
    <div>
      <h1 className="text-2xl font-bold text-text-primary mb-6">Downloads</h1>
      <Card className="mb-6">
        <p className="text-text-secondary text-sm mb-4">Download the OrvixEM installer and related tools. The installer script automatically detects your OS and architecture.</p>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="bg-bg-elevated rounded-lg p-4 text-center">
            <p className="text-2xl mb-2">🐧</p>
            <p className="font-medium text-text-primary">Linux (x86_64)</p>
            <p className="text-xs text-text-secondary mt-1">Recommended for production</p>
          </div>
          <div className="bg-bg-elevated rounded-lg p-4 text-center">
            <p className="text-2xl mb-2">🪟</p>
            <p className="font-medium text-text-primary">Windows (x86_64)</p>
            <p className="text-xs text-text-secondary mt-1">For development/testing</p>
          </div>
          <div className="bg-bg-elevated rounded-lg p-4 text-center">
            <p className="text-2xl mb-2">🍎</p>
            <p className="font-medium text-text-primary">macOS (ARM64)</p>
            <p className="text-xs text-text-secondary mt-1">Apple Silicon support</p>
          </div>
        </div>
        <div className="mt-4 bg-bg-elevated rounded-lg p-4">
          <p className="text-sm font-medium text-text-primary mb-2">Quick Install</p>
          <code className="text-xs text-accent break-all">curl -sSL https://get.orvix.email | bash</code>
        </div>
      </Card>
    </div>
  )
}
