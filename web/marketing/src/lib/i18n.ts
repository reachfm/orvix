/**
 * Locale handling. The Launch Specification v1.0 §0.6 declares
 * English and Arabic as v1.0 locales, with RTL opt-in via ?rtl=1.
 *
 * The marketing site is delivered fully in English; Arabic
 * strings are intentionally left empty in v1.0 — the page falls
 * back to English for missing keys. This matches the spec's
 * "partial Arabic is acceptable" rule and avoids shipping
 * placeholder strings.
 *
 * No placeholder Lorem-ipsum; missing keys fall back to the key
 * itself, which surfaces the gap during review.
 */

export type Locale = "en" | "ar";

export const SUPPORTED_LOCALES: Locale[] = ["en", "ar"];

const STRINGS: Record<string, string> = {
  "nav.features": "Features",
  "nav.enterprise": "Enterprise",
  "nav.security": "Security",
  "nav.pricing": "Pricing",
  "nav.docs": "Docs",
  "nav.status": "Status",
  "nav.signin": "Sign in",
  "nav.startFree": "Start free",
  "footer.product": "Product",
  "footer.resources": "Resources",
  "footer.company": "Company",
  "footer.legal": "Legal",
  "footer.changelog": "Changelog",
  "footer.blog": "Blog",
  "footer.about": "About",
  "footer.contact": "Contact",
  "footer.careers": "Careers",
  "footer.terms": "Terms",
  "footer.privacy": "Privacy",
  "footer.aup": "AUP",
  "footer.cookies": "Cookies",
  "footer.dataAndPrivacy": "Data & privacy",
  "footer.copyright": "© Orvix, Inc.",
  "footer.tagline": "Email hosting for teams that need it to work.",
  "cta.startFree": "Start free",
  "cta.contactSales": "Contact sales",
  "cta.viewDocs": "Read the docs",
  "cta.viewPricing": "See pricing",
  "cta.signIn": "Sign in",
  "lang.en": "English",
  "lang.ar": "العربية",
  "cookie.bannerText":
    "We use a small set of strictly necessary and functional cookies to run this site. Analytics is opt-in. You can change your choice any time on the cookie policy page.",
  "cookie.accept": "Accept all",
  "cookie.reject": "Reject non-essential",
  "cookie.policy": "Cookie policy",
};

/** Look up a translated string with English fallback. */
export function t(key: string, locale: Locale = "en"): string {
  return STRINGS[key] ?? key;
}

/** Detect locale from the URL (?rtl=1 forces Arabic + dir=rtl). */
export function detectLocale(search: string): Locale {
  const params = new URLSearchParams(search);
  if (params.get("rtl") === "1") {
    return "ar";
  }
  return "en";
}

/** Map a locale to the BCP-47 lang attribute. */
export function htmlLang(locale: Locale): string {
  return locale;
}

/** Map a locale to document direction. */
export function htmlDir(locale: Locale): "ltr" | "rtl" {
  return locale === "ar" ? "rtl" : "ltr";
}
