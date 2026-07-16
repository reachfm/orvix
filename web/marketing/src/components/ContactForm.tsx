import { useState } from "react";
import { Mail, Building, MessageSquare, CheckCircle2 } from "lucide-react";
import { PORTAL_LOGIN } from "../lib/links";

type Subject = "sales" | "support" | "security" | "press" | "other";

/**
 * The marketing site is anonymous. There is no public POST
 * /api/v1/contact endpoint in the backend (verified in the
 * launch spec, §1 — marketing pages are static), so this form
 * composes a mailto: link that opens the visitor's mail client
 * pre-filled with their message. We DO NOT pretend the
 * submission succeeded.
 */
export default function ContactForm() {
  const [subject, setSubject] = useState<Subject>("sales");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [org, setOrg] = useState("");
  const [message, setMessage] = useState("");

  const addressBySubject: Record<Subject, string> = {
    sales: "sales@orvix.com",
    support: "support@orvix.com",
    security: "security@orvix.com",
    press: "press@orvix.com",
    other: "hello@orvix.com",
  };

  const body = `Name: ${name}\nEmail: ${email}\nOrganization: ${org}\n\n${message}`;
  const mailto = `mailto:${addressBySubject[subject]}?subject=${encodeURIComponent(
    `[Orvix] ${subject} inquiry`,
  )}&body=${encodeURIComponent(body)}`;

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        window.location.href = mailto;
      }}
      aria-label="Contact Orvix"
      style={{
        display: "grid",
        gap: "var(--sp-4)",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-lg)",
        padding: "var(--sp-6)",
      }}
    >
      <div
        style={{
          display: "flex",
          alignItems: "flex-start",
          gap: "var(--sp-3)",
          padding: "var(--sp-3) var(--sp-4)",
          background: "var(--bg-elevated)",
          border: "1px solid var(--border-default)",
          borderRadius: "var(--r-md)",
          color: "var(--text-secondary)",
          fontSize: "var(--fs-xs)",
        }}
      >
        <CheckCircle2
          size={16}
          style={{ flexShrink: 0, marginTop: 2, color: "var(--accent)" }}
          aria-hidden="true"
        />
        <span>
          We don&apos;t run a public contact API on the marketing site. Submitting
          this form opens your mail client with the message pre-filled and sends
          to the address that matches your subject.
        </span>
      </div>

      <div>
        <label htmlFor="subject" className="label">
          What is this about?
        </label>
        <select
          id="subject"
          className="input"
          value={subject}
          onChange={(e) => setSubject(e.target.value as Subject)}
        >
          <option value="sales">Sales — I want a quote or demo</option>
          <option value="support">Support — I need help with my account</option>
          <option value="security">
            Security — I&apos;m reporting a vulnerability (responsible disclosure)
          </option>
          <option value="press">Press / partnerships</option>
          <option value="other">Something else</option>
        </select>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 1fr",
          gap: "var(--sp-3)",
        }}
      >
        <div>
          <label htmlFor="name" className="label">
            Your name
          </label>
          <input
            id="name"
            className="input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
            autoComplete="name"
          />
        </div>
        <div>
          <label htmlFor="email" className="label">
            Your email
          </label>
          <input
            id="email"
            type="email"
            className="input"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            required
            autoComplete="email"
          />
        </div>
      </div>

      <div>
        <label htmlFor="org" className="label">
          Organization (optional)
        </label>
        <input
          id="org"
          className="input"
          value={org}
          onChange={(e) => setOrg(e.target.value)}
          autoComplete="organization"
        />
      </div>

      <div>
        <label htmlFor="message" className="label">
          Your message
        </label>
        <textarea
          id="message"
          className="input"
          rows={6}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          required
        />
      </div>

      <div
        style={{
          display: "flex",
          flexWrap: "wrap",
          gap: "var(--sp-3)",
          alignItems: "center",
          justifyContent: "space-between",
        }}
      >
        <button type="submit" className="btn btn-primary btn-lg">
          <Mail size={16} aria-hidden="true" />
          Open in mail client
        </button>
        <p
          style={{
            fontSize: "var(--fs-xs)",
            color: "var(--text-muted)",
            margin: 0,
          }}
        >
          Or email us directly at{" "}
          <a href="mailto:hello@orvix.com">hello@orvix.com</a>. For account
          questions, please{" "}
          <a href={PORTAL_LOGIN} style={{ color: "var(--accent)" }}>
            sign in
          </a>{" "}
          and use in-product support.
        </p>
      </div>
    </form>
  );
}

export function ContactSidebar() {
  return (
    <aside
      style={{
        display: "grid",
        gap: "var(--sp-4)",
      }}
    >
      <SidebarItem
        icon={Building}
        title="Sales"
        body="Quotes, demos, and procurement questions."
        address="sales@orvix.com"
      />
      <SidebarItem
        icon={MessageSquare}
        title="Support"
        body="Account, billing, and product questions. Existing customers can also use in-product support."
        address="support@orvix.com"
      />
      <SidebarItem
        icon={Mail}
        title="Security disclosure"
        body="Coordinated disclosure for reproducible vulnerabilities."
        address="security@orvix.com"
      />
    </aside>
  );
}

function SidebarItem({
  icon: Icon,
  title,
  body,
  address,
}: {
  icon: typeof Building;
  title: string;
  body: string;
  address: string;
}) {
  return (
    <div
      className="card-static"
      style={{ display: "flex", flexDirection: "column", gap: "var(--sp-2)" }}
    >
      <div
        style={{
          display: "inline-flex",
          alignItems: "center",
          gap: "var(--sp-2)",
          color: "var(--accent)",
        }}
      >
        <Icon size={16} aria-hidden="true" />
        <span
          style={{
            fontWeight: 600,
            color: "var(--text-primary)",
            fontSize: "var(--fs-sm)",
          }}
        >
          {title}
        </span>
      </div>
      <p
        style={{
          fontSize: "var(--fs-sm)",
          color: "var(--text-secondary)",
          margin: 0,
          lineHeight: 1.6,
        }}
      >
        {body}
      </p>
      <a
        href={`mailto:${address}`}
        style={{
          fontSize: "var(--fs-sm)",
          color: "var(--accent)",
          fontWeight: 600,
        }}
      >
        {address}
      </a>
    </div>
  );
}
