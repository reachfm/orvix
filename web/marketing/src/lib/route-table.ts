/**
 * Route table — the single source of truth for every public
 * marketing route. Pages are loaded lazily from src/pages/*.
 *
 * Adding a new page? Add the entry here AND register a route in
 * App.tsx. The unit test in tests/unit/route-table.test.ts asserts
 * every ROUTES entry is reachable via the React Router config.
 */

import { lazy, type ComponentType } from "react";

export interface RouteEntry {
  path: string;
  page: ComponentType;
  /** SEO meta is sourced from lib/seo.ts ROUTE_SEO. We keep an
   * explicit reference here so missing entries fail typecheck. */
  seoKey: keyof typeof import("./seo").ROUTE_SEO;
}

export const HOME = "/";
export const PRICING = "/pricing";
export const FEATURES = "/features";
export const ENTERPRISE = "/enterprise";
export const SECURITY = "/security";
export const DOCS = "/docs";
export const API = "/api";
export const STATUS = "/status";
export const ABOUT = "/about";
export const CONTACT = "/contact";
export const BLOG = "/blog";
export const BLOG_WELCOME = "/blog/welcome-to-orvix";
export const LEGAL = "/legal";
export const LEGAL_TERMS = "/legal/terms";
export const LEGAL_PRIVACY = "/legal/privacy";
export const LEGAL_AUP = "/legal/aup";
export const LEGAL_COOKIES = "/legal/cookies";
export const LEGAL_DATA = "/legal/data-and-privacy";

/** All public marketing paths. Used by the sitemap builder. */
export const PUBLIC_PATHS: string[] = [
  HOME,
  PRICING,
  FEATURES,
  ENTERPRISE,
  SECURITY,
  DOCS,
  API,
  STATUS,
  ABOUT,
  CONTACT,
  BLOG,
  BLOG_WELCOME,
  LEGAL,
  LEGAL_TERMS,
  LEGAL_PRIVACY,
  LEGAL_AUP,
  LEGAL_COOKIES,
  LEGAL_DATA,
];

/** Document paths (under /docs/) that exist in docs/customer/. */
export const DOC_PATHS: string[] = [
  "getting-started",
  "domains",
  "mailboxes",
  "aliases",
  "groups",
  "rules",
  "signatures",
  "vacation",
  "forwarding",
  "password-reset",
  "mfa",
  "api-keys",
  "api",
  "billing-provider",
  "plans",
  "quotas",
  "subscription-states",
  "usage",
  "domain-verification",
  "dns-troubleshooting",
  "mx",
  "spf",
  "dkim",
  "dmarc",
  "security",
  "support",
  "troubleshooting",
  "invitations",
  "roles",
  "ownership-transfer",
  "organization",
  "privacy",
  "webmail",
  "status",
  "index",
];

/** Lazy page loaders keyed by ROUTE_SEO. */
export const PAGE_LOADERS: Record<string, ComponentType> = {
  [HOME]: lazy(() => import("../pages/Home")),
  [PRICING]: lazy(() => import("../pages/Pricing")),
  [FEATURES]: lazy(() => import("../pages/Features")),
  [ENTERPRISE]: lazy(() => import("../pages/Enterprise")),
  [SECURITY]: lazy(() => import("../pages/Security")),
  [DOCS]: lazy(() => import("../pages/Docs")),
  [API]: lazy(() => import("../pages/Api")),
  [STATUS]: lazy(() => import("../pages/Status")),
  [ABOUT]: lazy(() => import("../pages/About")),
  [CONTACT]: lazy(() => import("../pages/Contact")),
  [BLOG]: lazy(() => import("../pages/Blog")),
  [BLOG_WELCOME]: lazy(() => import("../pages/BlogWelcome")),
  [LEGAL]: lazy(() => import("../pages/LegalIndex")),
  [LEGAL_TERMS]: lazy(() => import("../pages/LegalTerms")),
  [LEGAL_PRIVACY]: lazy(() => import("../pages/LegalPrivacy")),
  [LEGAL_AUP]: lazy(() => import("../pages/LegalAup")),
  [LEGAL_COOKIES]: lazy(() => import("../pages/LegalCookies")),
  [LEGAL_DATA]: lazy(() => import("../pages/LegalData")),
  "*": lazy(() => import("../pages/NotFound")),
};
