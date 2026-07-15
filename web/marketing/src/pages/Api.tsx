import { Link } from "react-router-dom";
import { Code2, ShieldCheck, Zap, KeyRound, Webhook } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import Illustration from "../components/Illustration";

const ENDPOINTS = [
  {
    method: "GET",
    path: "/api/v1/health",
    summary: "Liveness probe. Returns 200 with a JSON body when the service is up.",
  },
  {
    method: "GET",
    path: "/api/v1/billing/plans",
    summary: "Plan catalog — same numbers the marketing site renders.",
  },
  {
    method: "POST",
    path: "/api/v1/auth/signup",
    summary: "Create an account. Used by the /signup page in the customer portal.",
  },
  {
    method: "POST",
    path: "/api/v1/auth/login",
    summary: "Authenticate with email + password. Returns an access token in a __Host-orvix_session cookie.",
  },
  {
    method: "POST",
    path: "/api/v1/auth/mfa/verify",
    summary: "Complete MFA challenge. Returns a fresh access token on success.",
  },
  {
    method: "GET",
    path: "/api/v1/me",
    summary: "Currently-authenticated user. Returns the user record and organization membership.",
  },
  {
    method: "GET",
    path: "/api/v1/me/mailboxes",
    summary: "Mailboxes the caller can see, paginated.",
  },
  {
    method: "POST",
    path: "/api/v1/me/messages",
    summary: "Send a message. Idempotent on the Idempotency-Key header.",
  },
  {
    method: "POST",
    path: "/api/v1/enterprise/domains",
    summary: "Add a domain to the current organization. Returns the verification challenge.",
  },
  {
    method: "POST",
    path: "/api/v1/enterprise/mailboxes",
    summary: "Create a mailbox. Returns the mailbox record and a one-time setup URL.",
  },
];

const METHOD_COLOR: Record<string, string> = {
  GET: "var(--info)",
  POST: "var(--success)",
  PUT: "var(--warning)",
  PATCH: "var(--warning)",
  DELETE: "var(--danger)",
};

export default function Api() {
  return (
    <>
      <SEO path="/api" />

      <Hero
        eyebrow="API"
        heading="A REST API for the whole product"
        subheading="JSON over HTTPS, bearer-token auth, an OpenAPI 3.0 spec, idempotent writes, and a documented rate limit on every endpoint."
        primaryCta={{ to: "/docs/api", label: "Read the API guide" }}
        secondaryCta={{ to: "/contact", label: "Talk to engineering" }}
        illustration={<Illustration variant="api-explorer" height={360} />}
      />

      <Section
        alt
        eyebrow="Quick start"
        heading="Three calls to your first message"
        lede="Authenticate, look up a mailbox, send. The full reference is in the API guide."
      >
        <Container width="narrow">
          <Code language="bash" caption="Sign in">
            {`curl -X POST https://app.orvix.com/api/v1/auth/login \\
  -H 'Content-Type: application/json' \\
  -c cookies.txt \\
  -d '{
    "email": "you@example.com",
    "password": "••••••••"
  }'`}
          </Code>
          <Code language="bash" caption="Look up a mailbox">
            {`curl https://app.orvix.com/api/v1/me/mailboxes \\
  -b cookies.txt`}
          </Code>
          <Code language="bash" caption="Send a message">
            {`curl -X POST https://app.orvix.com/api/v1/me/messages \\
  -H 'Content-Type: application/json' \\
  -H 'Idempotency-Key: 01HXYZB3RAQ8T3C9K3F4S6D2XM' \\
  -b cookies.txt \\
  -d '{
    "from": "alice@acme.example",
    "to": ["bob@acme.example"],
    "subject": "Q3 launch plan",
    "text": "Sharing the launch plan…"
  }'`}
          </Code>
        </Container>
      </Section>

      <Section
        eyebrow="Endpoints"
        heading="The public surface, in one place"
        lede="A short list of the most-used endpoints. The full list is in the OpenAPI spec linked from the docs page."
      >
        <Container>
          <div
            style={{
              background: "var(--bg-canvas)",
              border: "1px solid var(--border-default)",
              borderRadius: "var(--r-lg)",
              overflow: "hidden",
            }}
          >
            <table
              style={{
                width: "100%",
                borderCollapse: "collapse",
                fontSize: "var(--fs-sm)",
              }}
            >
              <thead>
                <tr>
                  <th
                    scope="col"
                    style={{
                      textAlign: "left",
                      padding: "var(--sp-3) var(--sp-4)",
                      borderBottom: "1px solid var(--border-default)",
                      color: "var(--text-secondary)",
                      fontWeight: 600,
                      width: "12%",
                    }}
                  >
                    Method
                  </th>
                  <th
                    scope="col"
                    style={{
                      textAlign: "left",
                      padding: "var(--sp-3) var(--sp-4)",
                      borderBottom: "1px solid var(--border-default)",
                      color: "var(--text-secondary)",
                      fontWeight: 600,
                      width: "36%",
                    }}
                  >
                    Path
                  </th>
                  <th
                    scope="col"
                    style={{
                      textAlign: "left",
                      padding: "var(--sp-3) var(--sp-4)",
                      borderBottom: "1px solid var(--border-default)",
                      color: "var(--text-secondary)",
                      fontWeight: 600,
                    }}
                  >
                    What it does
                  </th>
                </tr>
              </thead>
              <tbody>
                {ENDPOINTS.map((ep) => (
                  <tr key={ep.method + ep.path}>
                    <td
                      style={{
                        padding: "var(--sp-3) var(--sp-4)",
                        borderBottom: "1px solid var(--border-subtle)",
                        color: METHOD_COLOR[ep.method] ?? "var(--text-primary)",
                        fontFamily: "monospace",
                        fontWeight: 700,
                      }}
                    >
                      {ep.method}
                    </td>
                    <td
                      style={{
                        padding: "var(--sp-3) var(--sp-4)",
                        borderBottom: "1px solid var(--border-subtle)",
                        fontFamily: "monospace",
                        color: "var(--text-primary)",
                      }}
                    >
                      {ep.path}
                    </td>
                    <td
                      style={{
                        padding: "var(--sp-3) var(--sp-4)",
                        borderBottom: "1px solid var(--border-subtle)",
                        color: "var(--text-secondary)",
                      }}
                    >
                      {ep.summary}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Container>
      </Section>

      <Section
        alt
        eyebrow="Design choices"
        heading="Why the API looks the way it does"
        lede="The boring decisions that make the API predictable to use and easy to migrate away from."
      >
        <div
          className="three-col"
          style={{
            display: "grid",
            gridTemplateColumns: "repeat(2, 1fr)",
            gap: "var(--sp-4)",
          }}
        >
          <Design icon={ShieldCheck} title="Auth and sessions">
            The portal host (app.orvix.com) is the only place that holds the
            __Host-orvix_session cookie. The marketing site never sees it.
            Bearer tokens are available for service-to-service use, and we
            document how to rotate them.
          </Design>
          <Design icon={KeyRound} title="Idempotency">
            Every POST that creates a resource accepts an
            <code> Idempotency-Key</code> header. Retries with the same key
            return the original response. Documented per-endpoint.
          </Design>
          <Design icon={Zap} title="Rate limits">
            Per-API-key limits, returned in the response headers
            (<code>X-RateLimit-Limit</code>, <code>X-RateLimit-Remaining</code>).
            429 responses include <code>Retry-After</code>.
          </Design>
          <Design icon={Webhook} title="Webhooks">
            Sign webhook payloads with HMAC-SHA256 using the secret shown
            when you create the webhook. The signature is in the{" "}
            <code>X-Orvix-Signature</code> header.
          </Design>
        </div>
        <style>{`@media (max-width: 880px) { .three-col { grid-template-columns: 1fr !important; } }`}</style>
      </Section>

      <Section bordered>
        <div
          style={{
            background: "var(--bg-canvas)",
            border: "1px solid var(--border-default)",
            borderRadius: "var(--r-lg)",
            padding: "var(--sp-6)",
            display: "flex",
            gap: "var(--sp-3)",
            alignItems: "flex-start",
            color: "var(--text-secondary)",
            fontSize: "var(--fs-sm)",
            lineHeight: 1.7,
          }}
        >
          <Code2
            size={20}
            aria-hidden="true"
            style={{ color: "var(--accent)", flexShrink: 0, marginTop: 2 }}
          />
          <div>
            <p style={{ margin: 0 }}>
              The OpenAPI 3.0 spec for the full API is in the customer docs.
              It is generated from the Go handler code, so it never drifts.
            </p>
            <p style={{ margin: "var(--sp-3) 0 0" }}>
              <Link
                to="/docs/api"
                style={{
                  color: "var(--accent)",
                  fontWeight: 600,
                  textDecoration: "none",
                }}
              >
                Read the API guide →
              </Link>
            </p>
          </div>
        </div>
      </Section>
    </>
  );
}

function Code({
  children,
  caption,
  language,
}: {
  children: string;
  caption?: string;
  language: string;
}) {
  return (
    <figure
      style={{
        margin: "0 0 var(--sp-4) 0",
        background: "var(--bg-canvas)",
        border: "1px solid var(--border-default)",
        borderRadius: "var(--r-md)",
        overflow: "hidden",
      }}
    >
      {caption && (
        <figcaption
          style={{
            padding: "var(--sp-2) var(--sp-4)",
            background: "var(--bg-elevated)",
            borderBottom: "1px solid var(--border-default)",
            fontSize: "var(--fs-xs)",
            color: "var(--text-muted)",
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            fontFamily: "monospace",
          }}
        >
          <span>{caption}</span>
          <span aria-hidden="true">{language}</span>
        </figcaption>
      )}
      <pre
        style={{
          margin: 0,
          padding: "var(--sp-4)",
          overflowX: "auto",
          color: "var(--text-primary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.6,
        }}
      >
        <code>{children}</code>
      </pre>
    </figure>
  );
}

function Design({
  icon: Icon,
  title,
  children,
}: {
  icon: typeof Code2;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <article
      className="card-static"
      style={{ display: "flex", flexDirection: "column", gap: "var(--sp-3)" }}
    >
      <span
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 36,
          height: 36,
          borderRadius: "var(--r-md)",
          background: "var(--accent-glow)",
          color: "var(--accent)",
        }}
        aria-hidden="true"
      >
        <Icon size={18} />
      </span>
      <h3
        style={{
          fontSize: "var(--fs-lg)",
          margin: 0,
          color: "var(--text-primary)",
        }}
      >
        {title}
      </h3>
      <p
        style={{
          margin: 0,
          color: "var(--text-secondary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.7,
        }}
      >
        {children}
      </p>
    </article>
  );
}
