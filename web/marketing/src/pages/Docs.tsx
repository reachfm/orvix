import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { Search, BookOpen, ArrowRight } from "lucide-react";
import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import DocLinkCard from "../components/DocLinkCard";
import { DOCS } from "../data/docs-index";

export default function Docs() {
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return DOCS;
    return DOCS.filter(
      (d) =>
        d.title.toLowerCase().includes(q) ||
        d.summary.toLowerCase().includes(q) ||
        d.category.toLowerCase().includes(q),
    );
  }, [query]);

  const categories = useMemo(() => {
    const out: Record<string, typeof DOCS> = {};
    for (const d of filtered) {
      out[d.category] = out[d.category] ?? [];
      out[d.category].push(d);
    }
    return out;
  }, [filtered]);

  return (
    <>
      <SEO path="/docs" />

      <Hero
        eyebrow="Documentation"
        heading="Everything you need to run Orvix"
        subheading="Searchable guides for getting started, running mailboxes, configuring DNS, using the API, and operating the product. The full source lives in the customer docs directory; this page indexes it."
        primaryCta={{ to: "/docs/getting-started", label: "Start with Getting started" }}
        secondaryCta={{ to: "/api", label: "See the API one-pager" }}
      />

      <Section tight>
        <Container width="narrow">
          <label htmlFor="doc-search" className="sr-only">
            Search the docs
          </label>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: "var(--sp-2)",
              background: "var(--bg-canvas)",
              border: "1px solid var(--border-default)",
              borderRadius: "var(--r-md)",
              padding: "var(--sp-3) var(--sp-4)",
            }}
          >
            <Search size={16} aria-hidden="true" style={{ color: "var(--text-muted)" }} />
            <input
              id="doc-search"
              type="search"
              placeholder="Search the docs…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              style={{
                background: "transparent",
                border: "none",
                outline: "none",
                color: "var(--text-primary)",
                fontSize: "var(--fs-base)",
                width: "100%",
                fontFamily: "inherit",
              }}
            />
          </div>
          <p
            style={{
              marginTop: "var(--sp-3)",
              color: "var(--text-muted)",
              fontSize: "var(--fs-sm)",
            }}
          >
            {filtered.length} of {DOCS.length} docs shown.
          </p>
        </Container>
      </Section>

      <Section alt>
        {Object.entries(categories).map(([category, entries]) => (
          <div key={category} style={{ marginBottom: "var(--sp-7)" }}>
            <h2
              style={{
                fontSize: "var(--fs-lg)",
                color: "var(--text-primary)",
                marginBottom: "var(--sp-3)",
                display: "flex",
                alignItems: "center",
                gap: "var(--sp-2)",
              }}
            >
              <BookOpen size={18} aria-hidden="true" style={{ color: "var(--accent)" }} />
              {category}
            </h2>
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))",
                gap: "var(--sp-3)",
              }}
            >
              {entries.map((doc) => (
                <DocLinkCard
                  key={doc.slug}
                  title={doc.title}
                  body={doc.summary}
                  href={`/docs/${doc.slug}`}
                />
              ))}
            </div>
          </div>
        ))}
        {filtered.length === 0 && (
          <Container width="narrow">
            <p
              style={{
                color: "var(--text-muted)",
                textAlign: "center",
                padding: "var(--sp-6) 0",
              }}
            >
              No docs matched <strong style={{ color: "var(--text-primary)" }}>&ldquo;{query}&rdquo;</strong>.
              Try a different search or{" "}
              <Link to="/contact" style={{ color: "var(--accent)" }}>
                ask the team
              </Link>
              .
            </p>
          </Container>
        )}
      </Section>

      <Section bordered>
        <div
          style={{
            background: "var(--bg-canvas)",
            border: "1px solid var(--border-default)",
            borderRadius: "var(--r-lg)",
            padding: "var(--sp-6)",
            display: "grid",
            gridTemplateColumns: "1fr 1fr",
            gap: "var(--sp-5)",
          }}
          className="docs-help"
        >
          <div>
            <h3
              style={{
                fontSize: "var(--fs-lg)",
                margin: 0,
                color: "var(--text-primary)",
              }}
            >
              The full source of these docs
            </h3>
            <p
              style={{
                marginTop: "var(--sp-2)",
                color: "var(--text-secondary)",
                fontSize: "var(--fs-sm)",
                lineHeight: 1.7,
              }}
            >
              Every doc on this page is a markdown file in{" "}
              <code style={{ color: "var(--text-primary)" }}>docs/customer/</code>{" "}
              in the Orvix repository. The marketing site is a thin
              index over those files — the canonical text lives in the
              repo, where it&apos;s reviewed in code review and version-controlled.
            </p>
            <p style={{ marginTop: "var(--sp-3)" }}>
              <Link
                to="/contact"
                style={{
                  color: "var(--accent)",
                  fontSize: "var(--fs-sm)",
                  fontWeight: 600,
                  display: "inline-flex",
                  alignItems: "center",
                  gap: "var(--sp-1)",
                }}
              >
                Suggest an edit
                <ArrowRight size={14} aria-hidden="true" />
              </Link>
            </p>
          </div>
          <div>
            <h3
              style={{
                fontSize: "var(--fs-lg)",
                margin: 0,
                color: "var(--text-primary)",
              }}
            >
              Need more than docs?
            </h3>
            <p
              style={{
                marginTop: "var(--sp-2)",
                color: "var(--text-secondary)",
                fontSize: "var(--fs-sm)",
                lineHeight: 1.7,
              }}
            >
              The API one-pager has a quick-reference for the REST endpoints.
              The security page documents our disclosure policy. The
              contact page has the right address for every team.
            </p>
            <ul
              style={{
                listStyle: "none",
                padding: 0,
                margin: "var(--sp-3) 0 0",
                display: "flex",
                flexWrap: "wrap",
                gap: "var(--sp-2)",
              }}
            >
              <li>
                <Link to="/api" className="btn btn-secondary btn-sm">
                  API
                </Link>
              </li>
              <li>
                <Link to="/security" className="btn btn-secondary btn-sm">
                  Security
                </Link>
              </li>
              <li>
                <Link to="/contact" className="btn btn-secondary btn-sm">
                  Contact
                </Link>
              </li>
            </ul>
          </div>
        </div>
        <style>{`@media (max-width: 880px) { .docs-help { grid-template-columns: 1fr !important; } }`}</style>
      </Section>
    </>
  );
}
