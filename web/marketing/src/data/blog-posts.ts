import type { BlogPostMeta } from "../components/BlogCard";

/**
 * Hard-coded blog posts. The Launch Specification v1.0 §1 notes
 * that the full blog "Requires backend implementation" — there is
 * no blog API in the Orvix server today. The marketing site ships
 * with these two announcement posts so the /blog page is never
 * empty. They are written in the same tone as the rest of the
 * site and only say things the spec already documents.
 *
 * Adding a post here is a content change, not a code change.
 */

export const BLOG_POSTS: BlogPostMeta[] = [
  {
    slug: "welcome-to-orvix",
    title: "Welcome to Orvix",
    excerpt:
      "Orvix is professional email hosting with custom domains, encrypted transport, and the admin controls a real team needs. Here's what we shipped and what's next.",
    date: "2026-07-15",
    readingMinutes: 4,
    author: "The Orvix team",
  },
];

export function getBlogPost(slug: string): BlogPostMeta | undefined {
  return BLOG_POSTS.find((p) => p.slug === slug);
}
