import { Helmet } from "react-helmet-async";
import { ROUTE_SEO, SITE_NAME, canonical, organizationLd, websiteLd } from "../lib/seo";

interface SEOProps {
  /** Path-only URL, e.g. "/pricing". Must exist in ROUTE_SEO. */
  path: keyof typeof ROUTE_SEO;
  /** Override the title/description if needed; otherwise uses ROUTE_SEO. */
  title?: string;
  description?: string;
  noindex?: boolean;
  jsonLd?: object;
}

export default function SEO({
  path,
  title,
  description,
  noindex,
  jsonLd,
}: SEOProps) {
  const fallback = ROUTE_SEO[path];
  const finalTitle = title ?? fallback?.title ?? `${SITE_NAME}`;
  const finalDescription = description ?? fallback?.description ?? "";
  const canonicalUrl = canonical(path);
  const ogImage = `${SITE_NAME && "https://orvix.com"}/og-default.png`;

  return (
    <Helmet>
      <html lang="en" />
      <title>{finalTitle}</title>
      <meta name="description" content={finalDescription} />
      <link rel="canonical" href={canonicalUrl} />
      {noindex ? (
        <meta name="robots" content="noindex,nofollow" />
      ) : (
        <meta name="robots" content="index,follow" />
      )}

      {/* Open Graph */}
      <meta property="og:type" content="website" />
      <meta property="og:title" content={finalTitle} />
      <meta property="og:description" content={finalDescription} />
      <meta property="og:url" content={canonicalUrl} />
      <meta property="og:image" content={ogImage} />
      <meta property="og:site_name" content={SITE_NAME} />
      <meta property="og:locale" content="en_US" />

      {/* Twitter */}
      <meta name="twitter:card" content="summary_large_image" />
      <meta name="twitter:title" content={finalTitle} />
      <meta name="twitter:description" content={finalDescription} />
      <meta name="twitter:image" content={ogImage} />

      {/* Structured data — emit organization + website JSON-LD on every page. */}
      <script type="application/ld+json">{organizationLd()}</script>
      <script type="application/ld+json">{websiteLd()}</script>
      {jsonLd && (
        <script type="application/ld+json">{JSON.stringify(jsonLd)}</script>
      )}
    </Helmet>
  );
}
