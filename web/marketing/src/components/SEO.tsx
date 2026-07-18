import { useEffect } from "react";
import { ROUTE_SEO, SITE_NAME, canonical, organizationLd, websiteLd } from "../lib/seo";

interface SEOProps {
  path: keyof typeof ROUTE_SEO;
  title?: string;
  description?: string;
  noindex?: boolean;
  jsonLd?: object;
}

export default function SEO({ path, title, description, noindex, jsonLd }: SEOProps) {
  const fallback = ROUTE_SEO[path];
  const finalTitle = title ?? fallback?.title ?? SITE_NAME;
  const finalDescription = description ?? fallback?.description ?? "";
  const canonicalUrl = canonical(path);
  const ogImage = "/favicon.svg";

  useEffect(() => {
    document.title = finalTitle;
    setMeta("name", "description", finalDescription);
    setMeta("name", "robots", noindex ? "noindex,nofollow" : "index,follow");
    setLink("canonical", canonicalUrl);
    setMeta("property", "og:type", "website");
    setMeta("property", "og:title", finalTitle);
    setMeta("property", "og:description", finalDescription);
    setMeta("property", "og:url", canonicalUrl);
    setMeta("property", "og:image", ogImage);
    setMeta("property", "og:site_name", SITE_NAME);
    setMeta("property", "og:locale", "en_US");
    setMeta("name", "twitter:card", "summary");
    setMeta("name", "twitter:title", finalTitle);
    setMeta("name", "twitter:description", finalDescription);
    setMeta("name", "twitter:image", ogImage);

    document.querySelectorAll("script[data-orvix-seo]").forEach((node) => node.remove());
    const payloads = [organizationLd(), websiteLd(), jsonLd ? JSON.stringify(jsonLd) : ""];
    for (const payload of payloads) {
      if (!payload) continue;
      const script = document.createElement("script");
      script.type = "application/ld+json";
      script.dataset.orvixSeo = "true";
      script.text = payload;
      document.head.appendChild(script);
    }
  }, [canonicalUrl, finalDescription, finalTitle, jsonLd, noindex, ogImage]);

  return null;
}

function setMeta(attribute: "name" | "property", key: string, content: string) {
  let node = document.head.querySelector<HTMLMetaElement>(`meta[${attribute}="${key}"]`);
  if (!node) {
    node = document.createElement("meta");
    node.setAttribute(attribute, key);
    document.head.appendChild(node);
  }
  node.content = content;
}

function setLink(rel: string, href: string) {
  let node = document.head.querySelector<HTMLLinkElement>(`link[rel="${rel}"]`);
  if (!node) {
    node = document.createElement("link");
    node.rel = rel;
    document.head.appendChild(node);
  }
  node.href = href;
}
