import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { MessageSquare, BookOpen, HeartPulse, ChevronDown, ChevronRight, ExternalLink, Send, Check } from "lucide-react";
import { api } from "../api";

const faqItems = [
  { q: "How do I add a new domain?", a: "Navigate to the Domains page and use the Add Domain button. Follow the DNS verification wizard to configure MX, SPF, DKIM, and DMARC records." },
  { q: "How do I create a new mailbox?", a: "Go to Mailboxes and click Create. Enter the email address, password, and assign a quota. The mailbox is ready immediately after creation." },
  { q: "What plans are available?", a: "Visit the Billing page to see all available plans. Each plan includes different limits for mailboxes, domains, storage, and send volume." },
  { q: "How do I reset my password?", a: "Go to Account Settings or Security page, enter your current password and a new password, then click Update Password." },
  { q: "Is two-factor authentication supported?", a: "Yes. Visit the Security page to enable MFA using any standard TOTP authenticator app like Google Authenticator or Authy." },
  { q: "How do I view my billing invoices?", a: "Go to the Invoices page to see your current plan, usage summary, and billing history. Invoice PDF download is coming soon." },
];

export default function SupportPage() {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [subject, setSubject] = useState("");
  const [message, setMessage] = useState("");
  const [submitted, setSubmitted] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setSubmitted(true);
    setName(""); setEmail(""); setSubject(""); setMessage("");
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">Support</h2>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <MessageSquare className="w-5 h-5 text-[#4F7CFF]" />
            <h3 className="text-lg font-medium text-white">Contact Support</h3>
          </div>

          {submitted ? (
            <div className="text-center py-6">
              <Check size={32} className="text-[#34D399] mx-auto mb-3" />
              <p className="text-white font-medium">Message Sent</p>
              <p className="text-sm text-[#8B92A8] mt-1">We'll get back to you soon.</p>
              <button onClick={() => setSubmitted(false)}
                className="mt-4 text-sm text-[#4F7CFF] hover:underline">Send another message</button>
            </div>
          ) : (
            <form onSubmit={handleSubmit} className="space-y-3">
              <div>
                <label className="block text-sm text-gray-400 mb-1">Name</label>
                <input required value={name} onChange={(e) => setName(e.target.value)}
                  className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
              </div>
              <div>
                <label className="block text-sm text-gray-400 mb-1">Email</label>
                <input required type="email" value={email} onChange={(e) => setEmail(e.target.value)}
                  className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
              </div>
              <div>
                <label className="block text-sm text-gray-400 mb-1">Subject</label>
                <input required value={subject} onChange={(e) => setSubject(e.target.value)}
                  className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
              </div>
              <div>
                <label className="block text-sm text-gray-400 mb-1">Message</label>
                <textarea required rows={4} value={message} onChange={(e) => setMessage(e.target.value)}
                  className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm resize-none" />
              </div>
              <button type="submit"
                className="flex items-center gap-2 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm hover:bg-[#3D6AE8]">
                <Send className="w-4 h-4" /> Send Message
              </button>
            </form>
          )}
        </div>

        <div className="space-y-6">
          <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
            <div className="flex items-center gap-3 mb-4">
              <BookOpen className="w-5 h-5 text-[#4F7CFF]" />
              <h3 className="text-lg font-medium text-white">Documentation</h3>
            </div>
            <div className="space-y-2">
              {[
                { label: "Getting Started Guide", url: "#" },
                { label: "Domain Configuration", url: "#" },
                { label: "Mailbox Management", url: "#" },
                { label: "DNS Setup Guide", url: "#" },
                { label: "API Reference", url: "#" },
                { label: "Security Best Practices", url: "#" },
              ].map((doc) => (
                <a key={doc.label} href={doc.url}
                  className="flex items-center justify-between p-3 bg-[#0C0E12] rounded hover:bg-[#1A1E26] transition-colors group">
                  <span className="text-sm text-[#E8EAF0]">{doc.label}</span>
                  <ExternalLink size={14} className="text-[#555D73] group-hover:text-[#4F7CFF] transition-colors" />
                </a>
              ))}
            </div>
          </div>

          <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
            <div className="flex items-center gap-3 mb-4">
              <HeartPulse className="w-5 h-5 text-[#4F7CFF]" />
              <h3 className="text-lg font-medium text-white">System Status</h3>
            </div>
            <div className="flex items-center gap-2 mb-3">
              <span className="w-2 h-2 rounded-full bg-[#34D399]" />
              <span className="text-sm text-[#E8EAF0]">All Systems Operational</span>
            </div>
            <p className="text-xs text-[#555D73]">
              All services are running normally. If you experience issues, please contact support.
            </p>
          </div>
        </div>
      </div>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <h3 className="text-lg font-medium text-white mb-4">Frequently Asked Questions</h3>
        <div className="space-y-2">
          {faqItems.map((item, idx) => (
            <div key={idx} className="bg-[#0C0E12] rounded-lg overflow-hidden">
              <button onClick={() => setExpanded(expanded === idx ? null : idx)}
                className="w-full flex items-center justify-between p-4 text-left hover:bg-[#1A1E26] transition-colors">
                <span className="text-sm text-[#E8EAF0]">{item.q}</span>
                {expanded === idx ? <ChevronDown size={16} className="text-[#8B92A8]" /> : <ChevronRight size={16} className="text-[#8B92A8]" />}
              </button>
              {expanded === idx && (
                <div className="px-4 pb-4">
                  <p className="text-sm text-[#8B92A8]">{item.a}</p>
                </div>
              )}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
