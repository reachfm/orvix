import SEO from "../components/SEO";
import Hero from "../components/Hero";
import Section from "../components/Section";
import Container from "../components/Container";
import BlogCard from "../components/BlogCard";
import { BLOG_POSTS } from "../data/blog-posts";

export default function Blog() {
  return (
    <>
      <SEO path="/blog" />

      <Hero
        eyebrow="Blog"
        heading="Updates from the Orvix team"
        subheading="Product news, security disclosures, and engineering deep-dives. We post when there is something real to say."
      />

      <Section>
        <Container>
          {BLOG_POSTS.length === 0 ? (
            <EmptyState />
          ) : (
            <div
              style={{
                display: "grid",
                gridTemplateColumns: "repeat(auto-fit, minmax(280px, 1fr))",
                gap: "var(--sp-4)",
              }}
            >
              {BLOG_POSTS.map((post) => (
                <BlogCard key={post.slug} post={post} />
              ))}
            </div>
          )}
        </Container>
      </Section>
    </>
  );
}

function EmptyState() {
  return (
    <div
      style={{
        background: "var(--bg-canvas)",
        border: "1px dashed var(--border-default)",
        borderRadius: "var(--r-lg)",
        padding: "var(--sp-7) var(--sp-5)",
        textAlign: "center",
        color: "var(--text-secondary)",
      }}
    >
      <h2
        style={{
          fontSize: "var(--fs-lg)",
          margin: 0,
          color: "var(--text-primary)",
        }}
      >
        Updates coming soon
      </h2>
      <p
        style={{
          marginTop: "var(--sp-2)",
          marginBottom: 0,
          maxWidth: "50ch",
          marginLeft: "auto",
          marginRight: "auto",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.7,
        }}
      >
        We&apos;re just getting started. When we have something worth
        publishing — a release, a security advisory, an engineering write-up —
        it&apos;ll show up here. In the meantime, the changelog has the full
        release history.
      </p>
    </div>
  );
}
