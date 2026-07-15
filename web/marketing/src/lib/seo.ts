/**
 * SEO helpers — page metadata + canonical/OG construction.
 *
 * Every page must call useSeo() in <head> with a unique title and
 * description. The check-seo-tags.mjs script verifies this in CI
 * against a build of index.html, so a missing description fails
 * the build.
 */

export interface SeoMeta {
  title: string;
  description: string;
  /** Path-only URL, e.g. "/pricing". Used for canonical + OG. */
  path: string;
  /** Optional override of the OG image. */
  image?: string;
  /** Defaults to "website". */
  type?: "website" | "article";
  /** When true, sets robots="noindex,nofollow". Use for the 404 page. */
  noindex?: boolean;
}

export const SITE_NAME = "Orvix";
export const SITE_BASE_URL = "https://orvix.com";
export const DEFAULT_OG_IMAGE = "/og-default.png";
export const DEFAULT_LOCALE = "en_US";

/** Build a full URL from a path. */
export function absoluteUrl(path: string): string {
  if (!path.startsWith("/")) {
    return `${SITE_BASE_URL}/${path}`;
  }
  return `${SITE_BASE_URL}${path}`;
}

/** Standard <link rel="canonical"> href. */
export function canonical(path: string): string {
  return absoluteUrl(path);
}

/** Build a JSON-LD Organization payload. */
export function organizationLd(): string {
  return JSON.stringify({
    "@context": "https://schema.org",
    "@type": "Organization",
    name: SITE_NAME,
    url: SITE_BASE_URL,
    logo: `${SITE_BASE_URL}/logo.svg`,
    description:
      "Orvix is professional email hosting for teams that need it to work: custom domains, encrypted transport, and admin controls.",
    contactPoint: [
      {
        "@type": "ContactPoint",
        contactType: "customer support",
        email: "support@orvix.com",
        availableLanguage: ["English", "Arabic"],
      },
    ],
  });
}

/** Build a JSON-LD WebSite payload (with sitelinks search). */
export function websiteLd(): string {
  return JSON.stringify({
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: SITE_NAME,
    url: SITE_BASE_URL,
    inLanguage: ["en", "ar"],
    potentialAction: {
      "@type": "SearchAction",
      target: `${SITE_BASE_URL}/docs?q={search_term_string}`,
      "query-input": "required name=search_term_string",
    },
  });
}

/** Single page-level SEO descriptor used by the route table. */
export const ROUTE_SEO: Record<string, { title: string; description: string }> = {
  "/": {
    title: "Orvix — Email hosting for teams that need it to work",
    description:
      "Orvix is professional email hosting with custom domains, encrypted transport, DKIM, MFA, and admin controls. Start free with up to 5 mailboxes on 1 domain.",
  },
  "/pricing": {
    title: "Pricing — Orvix",
    description:
      "Orvix plans start free and scale from $9.99/mo. Custom domains, DKIM, MFA, and full audit log on every paid tier. No hidden fees.",
  },
  "/features": {
    title: "Features — Orvix",
    description:
      "Custom domains, DKIM signing, MTA-STS policy, encrypted backups, MFA, audit log, and a full API. Everything Orvix includes on every plan.",
  },
  "/enterprise": {
    title: "Enterprise — Orvix",
    description:
      "Orvix Enterprise: up to 100 domains, 1,000 mailboxes, 1 TB storage, 100,000 sends per day, SSO, 99.99% SLA, and priority support.",
  },
  "/security": {
    title: "Security & compliance — Orvix",
    description:
      "How Orvix secures your mail: TLS 1.2 minimum, DKIM, DMARC, SPF, MTA-STS, encrypted backups, MFA, and a full audit log.",
  },
  "/docs": {
    title: "Documentation — Orvix",
    description:
      "Guides for getting started with Orvix, setting up DNS, configuring DKIM, managing mailboxes, and using the API.",
  },
  "/api": {
    title: "API — Orvix",
    description:
      "Orvix REST API for managing domains, mailboxes, aliases, and sending mail. Bearer-token auth, JSON, OpenAPI 3.0.",
  },
  "/status": {
    title: "Status — Orvix",
    description:
      "Live status of Orvix services: SMTP inbound, SMTP outbound, IMAP, webmail, API, and billing. 90-day incident history.",
  },
  "/about": {
    title: "About — Orvix",
    description:
      "Orvix is a professional email hosting company. Learn about our team, our principles, and how we got here.",
  },
  "/contact": {
    title: "Contact — Orvix",
    description:
      "Get in touch with Orvix. Sales, support, security disclosure, and press. We respond within one business day.",
  },
  "/blog": {
    title: "Blog — Orvix",
    description: "Updates from the Orvix team. Product news, security disclosures, and engineering deep-dives.",
  },
  "/blog/welcome-to-orvix": {
    title: "Welcome to Orvix — Orvix",
    description:
      "Orvix is a new way to run professional email. Custom domains, encrypted transport, and admin controls on a clean plan structure.",
  },
  "/legal": {
    title: "Legal — Orvix",
    description: "Orvix legal documents: terms, privacy, acceptable use, cookies, and data and privacy.",
  },
  "/legal/terms": {
    title: "Terms of service — Orvix",
    description:
      "Orvix terms of service: what we provide, what you agree to, and how we handle disputes.",
  },
  "/legal/privacy": {
    title: "Privacy policy — Orvix",
    description:
      "Orvix privacy policy: what personal data we collect, why, how long we keep it, and how you can ask for it back.",
  },
  "/legal/aup": {
    title: "Acceptable use policy — Orvix",
    description:
      "What you can and cannot do on Orvix: spam, abuse, content rules, and enforcement.",
  },
  "/legal/cookies": {
    title: "Cookie policy — Orvix",
    description:
      "How Orvix uses cookies and local storage. Strictly necessary, functional, and analytics cookies are listed with retention.",
  },
  "/legal/data-and-privacy": {
    title: "Data and privacy — Orvix",
    description:
      "Where your data lives, who can access it, how long we keep it, and how to exercise your data subject rights under GDPR.",
  },
};
