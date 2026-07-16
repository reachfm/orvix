import { Link } from "react-router-dom";
import { ArrowRight } from "lucide-react";

export interface BlogPostMeta {
  slug: string;
  title: string;
  excerpt: string;
  date: string;
  readingMinutes: number;
  author: string;
}

interface BlogCardProps {
  post: BlogPostMeta;
}

export default function BlogCard({ post }: BlogCardProps) {
  return (
    <article className="card" aria-label={post.title}>
      <p
        style={{
          fontSize: "var(--fs-xs)",
          color: "var(--text-faint)",
          textTransform: "uppercase",
          letterSpacing: "0.08em",
          marginBottom: "var(--sp-2)",
        }}
      >
        {new Date(post.date).toLocaleDateString("en-US", {
          year: "numeric",
          month: "long",
          day: "numeric",
        })}{" "}
        · {post.readingMinutes} min read
      </p>
      <h3
        style={{
          fontSize: "var(--fs-lg)",
          color: "var(--text-primary)",
          margin: 0,
        }}
      >
        <Link
          to={`/blog/${post.slug}`}
          style={{ color: "inherit", textDecoration: "none" }}
        >
          {post.title}
        </Link>
      </h3>
      <p
        style={{
          margin: "var(--sp-3) 0 0",
          color: "var(--text-secondary)",
          fontSize: "var(--fs-sm)",
          lineHeight: 1.6,
        }}
      >
        {post.excerpt}
      </p>
      <p
        style={{
          margin: "var(--sp-4) 0 0",
          fontSize: "var(--fs-xs)",
          color: "var(--text-muted)",
        }}
      >
        By {post.author}
      </p>
      <p style={{ marginTop: "var(--sp-4)" }}>
        <Link
          to={`/blog/${post.slug}`}
          style={{
            color: "var(--accent)",
            fontSize: "var(--fs-sm)",
            fontWeight: 600,
            textDecoration: "none",
            display: "inline-flex",
            alignItems: "center",
            gap: "var(--sp-1)",
          }}
        >
          Read the post <ArrowRight size={14} aria-hidden="true" />
        </Link>
      </p>
    </article>
  );
}
