import { Code2, LockKeyhole, Route } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import Illustration from "../components/Illustration";
import { DOCS_BASE } from "../lib/links";

const ENDPOINTS = [
  { method: "GET", path: "/api/v1/health", summary: "Public service liveness probe." },
  { method: "GET", path: "/api/v1/billing/plans", summary: "Public plan catalog used by the customer experience." },
  { method: "POST", path: "/api/v1/auth/signup", summary: "Create a customer account and organization." },
  { method: "POST", path: "/api/v1/auth/login", summary: "Start an authenticated customer session or MFA challenge." },
  { method: "POST", path: "/api/v1/auth/mfa/verify", summary: "Complete an MFA login challenge." },
  { method: "GET", path: "/api/v1/me", summary: "Return the authenticated customer identity." },
  { method: "GET", path: "/api/v1/enterprise/domains", summary: "List domains in the authenticated tenant." },
  { method: "GET", path: "/api/v1/enterprise/mailboxes", summary: "List mailboxes in the authenticated tenant." },
];

export default function Api() {
  return (
    <>
      <SEO path="/api" />
      <Hero eyebrow="API" heading="The documented Orvix HTTP API" subheading="Public health and plan endpoints plus authenticated customer and enterprise operations. The OpenAPI document is the authority for request and response details." primaryCta={{ to: `${DOCS_BASE}/api`, label: "Read the API reference", external: true }} secondaryCta={{ to: "/contact", label: "Contact us" }} illustration={<Illustration variant="api-explorer" height={360} />} />

      <Section alt eyebrow="Public example" heading="Check service health" lede="This endpoint proves API liveness only. It is not a promise that every mail protocol or dependency is healthy.">
        <Container width="narrow"><pre className="card-static" style={{ overflowX: "auto" }}><code>{`curl -fsS https://app.orvix.com/api/v1/health`}</code></pre></Container>
      </Section>

      <Section eyebrow="Selected routes" heading="A verified subset" lede="These paths are registered by the server. Consult the OpenAPI reference for authentication, CSRF, fields, and errors.">
        <Container><div style={{ overflowX: "auto", border: "1px solid var(--border-default)", borderRadius: "var(--r-lg)" }}><table style={{ width: "100%", borderCollapse: "collapse" }}><thead><tr><th scope="col">Method</th><th scope="col">Path</th><th scope="col">Purpose</th></tr></thead><tbody>{ENDPOINTS.map((endpoint) => <tr key={endpoint.method + endpoint.path}><td><code>{endpoint.method}</code></td><td><code>{endpoint.path}</code></td><td>{endpoint.summary}</td></tr>)}</tbody></table></div></Container>
      </Section>

      <Section alt eyebrow="Security model" heading="Use the contract, not assumptions">
        <div className="two-col" style={{ display: "grid", gridTemplateColumns: "repeat(2, 1fr)", gap: "var(--sp-4)" }}>
          <Info icon={LockKeyhole} title="Authentication and CSRF">Customer and administrative routes are protected by the server's authentication and authorization middleware. Browser clients must follow the documented cookie and CSRF contract.</Info>
          <Info icon={Route} title="Tenant scoping">Enterprise routes resolve tenant identity from authenticated context. Client-supplied tenant identifiers are not a substitute for authorization.</Info>
        </div>
        <style>{`@media (max-width: 880px) { .two-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section bordered><Container width="narrow"><p style={{ margin: 0 }}><Code2 size={18} aria-hidden="true" /> The external <a href={`${DOCS_BASE}/api`}>API reference</a> is authoritative. This marketing page intentionally does not invent endpoint behavior.</p></Container></Section>
    </>
  );
}

function Info({ icon: Icon, title, children }: { icon: typeof Route; title: string; children: React.ReactNode }) {
  return <article className="card-static"><Icon size={20} aria-hidden="true" /><h3>{title}</h3><p style={{ color: "var(--text-secondary)", lineHeight: 1.6 }}>{children}</p></article>;
}
