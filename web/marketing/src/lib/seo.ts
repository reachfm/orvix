import seoData from "./seo-data.json";

export interface SeoMeta {
  title: string;
  description: string;
  path: string;
  image?: string;
  type?: "website" | "article";
  noindex?: boolean;
}

export const SITE_NAME = seoData.siteName;
export const SITE_BASE_URL = seoData.siteBaseUrl;
export const DEFAULT_OG_IMAGE = "/favicon.svg";
export const DEFAULT_LOCALE = "en_US";

export function absoluteUrl(path: string): string {
  return path.startsWith("/") ? `${SITE_BASE_URL}${path}` : `${SITE_BASE_URL}/${path}`;
}

export function canonical(path: string): string {
  return absoluteUrl(path);
}

export function organizationLd(): string {
  return JSON.stringify({
    "@context": "https://schema.org",
    "@type": "Organization",
    name: SITE_NAME,
    url: SITE_BASE_URL,
    logo: `${SITE_BASE_URL}/favicon.svg`,
    description: "Professional email hosting with custom domains, encrypted transport, and admin controls.",
    contactPoint: [{
      "@type": "ContactPoint",
      contactType: "customer support",
      email: "support@orvix.email",
      availableLanguage: ["English", "Arabic"],
    }],
  });
}

export function websiteLd(): string {
  return JSON.stringify({
    "@context": "https://schema.org",
    "@type": "WebSite",
    name: SITE_NAME,
    url: SITE_BASE_URL,
    inLanguage: ["en", "ar"],
  });
}

export const ROUTE_SEO: Record<string, { title: string; description: string }> = seoData.routes;
