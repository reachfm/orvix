import { useVirtualizer } from "@tanstack/react-virtual";
import { useRef, useState, useEffect } from "react";

type Folder = "inbox" | "sent" | "drafts" | "spam" | "trash" | "archive";

interface Email {
  id: string;
  from: string;
  subject: string;
  preview: string;
  date: string;
  unread: boolean;
}

interface EmailListProps {
  folder: Folder;
  selectedId: string | null;
  onSelect: (id: string) => void;
}

export default function EmailList({ folder, selectedId, onSelect }: EmailListProps) {
  const parentRef = useRef<HTMLDivElement>(null);
  const [emails, setEmails] = useState<Email[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    fetch(`/api/v1/queue?folder=${folder}`, {
      credentials: "include",
    })
      .then((res) => {
        if (!res.ok) throw new Error("Failed to fetch");
        return res.json();
      })
      .then((data: Email[]) => {
        setEmails(data);
        setLoading(false);
      })
      .catch(() => {
        setEmails([]);
        setLoading(false);
      });
  }, [folder]);

  const virtualizer = useVirtualizer({
    count: emails.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 72,
  });

  return (
    <div className="w-[420px] border-r border-[#2A2F3E] flex flex-col">
      <div className="p-3 border-b border-[#2A2F3E] bg-[#13161C]">
        <h2 className="text-sm font-semibold capitalize text-[#E8EAF0]">
          {folder}{loading ? " (loading...)" : ` (${emails.length})`}
        </h2>
      </div>

      {loading ? (
        <div className="flex-1 flex items-center justify-center">
          <p className="text-[#555D73] text-sm">Loading emails...</p>
        </div>
      ) : emails.length === 0 ? (
        <div className="flex-1 flex items-center justify-center">
          <p className="text-[#555D73] text-sm">No emails in {folder}</p>
        </div>
      ) : (
        <div ref={parentRef} className="flex-1 overflow-auto">
          <div
            style={{
              height: `${virtualizer.getTotalSize()}px`,
              width: "100%",
              position: "relative",
            }}
          >
            {virtualizer.getVirtualItems().map((virtualItem) => {
              const email = emails[virtualItem.index];
              return (
                <div
                  key={email.id}
                  onClick={() => onSelect(email.id)}
                  className={`absolute top-0 left-0 w-full px-4 py-3 border-b border-[#2A2F3E] cursor-pointer transition-colors ${
                    selectedId === email.id
                      ? "bg-[#222736]"
                      : email.unread
                        ? "bg-[#1A1E26]"
                        : "bg-[#13161C] hover:bg-[#1A1E26]"
                  }`}
                  style={{
                    height: `${virtualItem.size}px`,
                    transform: `translateY(${virtualItem.start}px)`,
                  }}
                >
                  <div className="flex justify-between items-start mb-1">
                    <span className={`text-sm ${email.unread ? "font-semibold text-[#E8EAF0]" : "text-[#8B92A8]"}`}>
                      {email.from}
                    </span>
                    <span className="text-xs text-[#555D73]">{email.date}</span>
                  </div>
                  <p className={`text-sm truncate ${email.unread ? "text-[#E8EAF0]" : "text-[#8B92A8]"}`}>
                    {email.subject}
                  </p>
                  <p className="text-xs text-[#555D73] truncate">{email.preview}</p>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
