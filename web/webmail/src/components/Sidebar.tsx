import {
  Inbox,
  Send,
  FileText,
  AlertTriangle,
  Trash2,
  Archive,
  PenSquare,
  Search,
} from "lucide-react";

type Folder = "inbox" | "sent" | "drafts" | "spam" | "trash" | "archive";

const folders: { id: Folder; label: string; icon: typeof Inbox }[] = [
  { id: "inbox", label: "Inbox", icon: Inbox },
  { id: "sent", label: "Sent", icon: Send },
  { id: "drafts", label: "Drafts", icon: FileText },
  { id: "spam", label: "Spam", icon: AlertTriangle },
  { id: "archive", label: "Archive", icon: Archive },
  { id: "trash", label: "Trash", icon: Trash2 },
];

interface SidebarProps {
  currentFolder: Folder;
  onFolderChange: (folder: Folder) => void;
  onCompose: () => void;
}

export default function Sidebar({ currentFolder, onFolderChange, onCompose }: SidebarProps) {
  return (
    <aside className="w-60 bg-[#13161C] border-r border-[#2A2F3E] flex flex-col">
      <div className="p-4 border-b border-[#2A2F3E]">
        <h1 className="text-lg font-semibold text-[#4F7CFF]">Orvix Mail</h1>
      </div>

      <button
        onClick={onCompose}
        className="mx-3 mt-3 mb-2 px-4 py-2.5 bg-[#4F7CFF] text-white rounded-lg flex items-center gap-2 hover:bg-[#6B93FF] transition-colors"
      >
        <PenSquare size={18} />
        <span className="text-sm font-medium">Compose</span>
      </button>

      <nav className="flex-1 px-2 py-2 space-y-0.5">
        {folders.map((f) => {
          const Icon = f.icon;
          const active = currentFolder === f.id;
          return (
            <button
              key={f.id}
              onClick={() => onFolderChange(f.id)}
              className={`w-full flex items-center gap-3 px-3 py-2 rounded-lg text-sm transition-colors ${
                active
                  ? "bg-[#222736] text-[#E8EAF0]"
                  : "text-[#8B92A8] hover:bg-[#1A1E26] hover:text-[#E8EAF0]"
              }`}
            >
              <Icon size={18} />
              <span>{f.label}</span>
            </button>
          );
        })}
      </nav>

      <div className="p-3 border-t border-[#2A2F3E]">
        <div className="relative">
          <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[#555D73]" />
          <input
            type="text"
            placeholder="Search mail..."
            className="w-full bg-[#1A1E26] border border-[#2A2F3E] rounded-lg pl-9 pr-3 py-2 text-sm text-[#E8EAF0] placeholder-[#555D73] outline-none focus:border-[#4F7CFF]"
          />
        </div>
      </div>
    </aside>
  );
}
