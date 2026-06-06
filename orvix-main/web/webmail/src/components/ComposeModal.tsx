import { X, Paperclip, Send } from "lucide-react";
import { useState } from "react";

interface ComposeModalProps {
  onClose: () => void;
}

export default function ComposeModal({ onClose }: ComposeModalProps) {
  const [to, setTo] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");

  return (
    <div className="fixed inset-0 bg-black/50 flex items-end justify-end p-4 z-50">
      <div className="w-[600px] h-[500px] bg-[#13161C] rounded-xl border border-[#2A2F3E] flex flex-col shadow-2xl">
        <div className="flex items-center justify-between px-4 py-3 border-b border-[#2A2F3E]">
          <h3 className="text-sm font-medium text-[#E8EAF0]">New Message</h3>
          <button onClick={onClose} className="p-1 rounded hover:bg-[#222736] text-[#8B92A8]">
            <X size={18} />
          </button>
        </div>

        <div className="px-4 py-2 border-b border-[#2A2F3E]">
          <input
            type="text"
            placeholder="To"
            value={to}
            onChange={(e) => setTo(e.target.value)}
            className="w-full bg-transparent text-sm text-[#E8EAF0] placeholder-[#555D73] outline-none"
          />
        </div>

        <div className="px-4 py-2 border-b border-[#2A2F3E]">
          <input
            type="text"
            placeholder="Subject"
            value={subject}
            onChange={(e) => setSubject(e.target.value)}
            className="w-full bg-transparent text-sm text-[#E8EAF0] placeholder-[#555D73] outline-none"
          />
        </div>

        <div className="flex-1 px-4 py-2">
          <textarea
            placeholder="Write your message..."
            value={body}
            onChange={(e) => setBody(e.target.value)}
            className="w-full h-full bg-transparent text-sm text-[#E8EAF0] placeholder-[#555D73] outline-none resize-none"
          />
        </div>

        <div className="flex items-center gap-2 px-4 py-3 border-t border-[#2A2F3E]">
          <button className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg flex items-center gap-2 text-sm hover:bg-[#6B93FF] transition-colors">
            <Send size={16} />
            Send
          </button>
          <button className="p-2 rounded-lg hover:bg-[#222736] text-[#8B92A8] transition-colors">
            <Paperclip size={18} />
          </button>
        </div>
      </div>
    </div>
  );
}
