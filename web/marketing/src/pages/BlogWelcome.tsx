import { Link } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import SEO from "../components/SEO";
import { PORTAL_SIGNUP } from "../lib/links";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import { getBlogPost } from "../data/blog-posts";

export default function BlogWelcome() {
  const post = getBlogPost("welcome-to-orvix");
  if (!post) {
    return <NotFoundFallback />;
  }
  return (
    <>
      <SEO
        path="/blog/welcome-to-orvix"
        title={post.title + " — Orvix"}
        description={post.excerpt}
        jsonLd={{
          "@context": "https://schema.org",
          "@type": "BlogPosting",
          headline: post.title,
          datePublished: post.date,
          author: { "@type": "Organization", name: post.author },
          publisher: { "@type": "Organization", name: "Orvix" },
        }}
      />

      <Section tight>
        <Container width="narrow">
          <p style={{ marginBottom: "var(--sp-3)" }}>
            <Link
              to="/blog"
              style={{
                color: "var(--accent)",
                fontSize: "var(--fs-sm)",
                display: "inline-flex",
                alignItems: "center",
                gap: "var(--sp-1)",
                textDecoration: "none",
              }}
            >
              <ArrowLeft size={14} aria-hidden="true" />
              All posts
            </Link>
          </p>
          <p
            style={{
              color: "var(--text-muted)",
              fontSize: "var(--fs-sm)",
              marginBottom: "var(--sp-2)",
            }}
          >
            {new Date(post.date).toLocaleDateString("en-US", {
              year: "numeric",
              month: "long",
              day: "numeric",
            })}{" "}
            · {post.readingMinutes} min read · {post.author}
          </p>
          <h1
            style={{
              fontSize: "var(--fs-4xl)",
              margin: 0,
              color: "var(--text-primary)",
            }}
          >
            {post.title}
          </h1>
          <p
            style={{
              marginTop: "var(--sp-3)",
              fontSize: "var(--fs-lg)",
              color: "var(--text-secondary)",
              lineHeight: 1.6,
            }}
          >
            {post.excerpt}
          </p>
        </Container>
      </Section>

      <Section>
        <Container width="narrow">
          <article className="prose">
            <p>
              Orvix is a new way to run professional email. We started it
              because every alternative we tried had the same shape: a great
              marketing site, a real product, and then — somewhere around the
              third renewal — a billing surprise, a support runaround, or a
              quiet price hike. We figured we could do better.
            </p>

            <h2>What we shipped in v1.0</h2>
            <p>
              The first release of Orvix is a complete mail platform: SMTP
              inbound and outbound, IMAP and JMAP, a modern webmail client, an
              admin console, and a REST API. The plan catalog is four tiers —
              Free, Starter, Business, and Enterprise — and the numbers are the
              same on the marketing site, in the launch spec, and in the
              billing API. There is no &quot;contact sales&quot; wall between you
              and the price.
            </p>

            <h3>The things that are not negotiable</h3>
            <ul>
              <li>Transport security and deployment-controlled storage protection.</li>
              <li>DKIM, SPF, and DMARC set up correctly by default.</li>
              <li>MFA on every account, with optional enforcement.</li>
              <li>An audit log of every administrative action.</li>
              <li>Open protocols (IMAP, JMAP, SMTP) and an OpenAPI spec.</li>
            </ul>

            <h2>What&apos;s next</h2>
            <p>
              Future capabilities will be documented here only after they ship.
            </p>

            <h2>Try it</h2>
            <p>
              The fastest way to see what we built is to{" "}
              <a href={PORTAL_SIGNUP} style={{ color: "var(--accent)" }}>
                sign up
              </a>
              , add a domain, and follow the DNS verification workflow. If you
              get stuck, the{" "}
              <Link to="/docs" style={{ color: "var(--accent)" }}>
                docs
              </Link>{" "}
              are searchable and the contact form on every page opens your
              mail client with the right address pre-filled.
            </p>

            <p>
              Thanks for reading. We&apos;ll be back when there&apos;s something
              new to say.
            </p>
            <p>
              — <em>The Orvix team</em>
            </p>
          </article>
        </Container>
      </Section>

      <Section bordered>
        <Container width="narrow" style={{ textAlign: "center" }}>
          <h2>More from the blog</h2>
          <p style={{ marginTop: "var(--sp-3)", color: "var(--text-secondary)" }}>Just this one post for now. We will publish the next one when there is something real to say.</p>
          <p style={{ marginTop: "var(--sp-4)", display: "flex", justifyContent: "center", gap: "var(--sp-3)" }}><Link to="/blog" className="btn btn-secondary">All posts</Link><a href={PORTAL_SIGNUP} className="btn btn-primary">Try Orvix</a></p>
        </Container>
      </Section>
    </>
  );
}

function NotFoundFallback() {
  return (
    <>
      <SEO path="/blog/welcome-to-orvix" noindex />
      <Hero
        heading="We couldn't find that post"
        subheading="It may have been moved or unpublished. Head back to the blog index for everything we have."
        primaryCta={{ to: "/blog", label: "All posts" }}
      />
    </>
  );
}
