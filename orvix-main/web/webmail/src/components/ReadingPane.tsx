import { Reply, Forward, Archive, Trash2, AlertTriangle } from "lucide-react";

interface ReadingPaneProps {
  emailId: string | null;
}

export default function ReadingPane({ emailId }: ReadingPaneProps) {
  if (!emailId) {
    return (
      <div className="flex-1 flex items-center justify-center bg-[#0C0E12]">
        <p className="text-[#555D73] text-sm">Select an email to read</p>
      </div>
    );
  }

  return (
    <div className="flex-1 flex flex-col bg-[#0C0E12]">
      <div className="flex items-center gap-2 p-3 border-b border-[#2A2F3E] bg-[#13161C]">
        <button className="p-2 rounded-lg hover:bg-[#222736] text-[#8B92A8] transition-colors">
          <Reply size={18} />
        </button>
        <button className="p-2 rounded-lg hover:bg-[#222736] text-[#8B92A8] transition-colors">
          <Forward size={18} />
        </button>
        <button className="p-2 rounded-lg hover:bg-[#222736] text-[#8B92A8] transition-colors">
          <Archive size={18} />
        </button>
        <button className="p-2 rounded-lg hover:bg-[#222736] text-[#8B92A8] transition-colors">
          <Trash2 size={18} />
        </button>
      </div>

      <div className="flex-1 overflow-auto p-6">
        <h1 className="text-xl font-semibold mb-4 text-[#E8EAF0]">Welcome to Orvix Webmail</h1>

        <div className="flex items-center gap-3 mb-6 p-3 bg-[#1A1E26] rounded-lg border border-[#2A2F3E]">
          <div className="w-10 h-10 rounded-full bg-[#4F7CFF] flex items-center justify-center text-white font-medium">
            O
          </div>
          <div>
            <p className="text-sm font-medium text-[#E8EAF0]">Orvix Team</p>
            <p className="text-xs text-[#555D73]">team@orvix.email</p>
          </div>
          <span className="ml-auto text-xs text-[#555D73]">Just now</span>
        </div>

        <div className="prose prose-invert max-w-none text-[#8B92A8] text-sm leading-relaxed">
          <p>Welcome to Orvix Webmail!</p>
          <p>
            Your professional email client is ready. Experience fast, secure, and intelligent
            email management with built-in AI assistance.
          </p>
          <p>Key features available:</p>
          <ul>
            <li>Virtualized email list - handles 100k+ emails</li>
            <li>Smart Compose AI - write emails 3x faster</li>
            <li>Calendar & Contacts integration</li>
            <li>End-to-end encryption support</li>
          </ul>
        </div>

        <div className="mt-6 p-3 bg-[#222736] rounded-lg border border-[#2A2F3E] flex items-center gap-2">
          <AlertTriangle size={16} className="text-[#FBBF24]" />
          <span className="text-xs text-[#FBBF24]">Images blocked. Load images</span>
        </div>
      </div>
    </div>
  );
}
