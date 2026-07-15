# Orvix Marketing Website

The public marketing site for Orvix. React 19 + Vite 6 + Tailwind v4 SPA,
matching the architecture of `web/webmail` and `web/admin`. The build
output is committed to `release/marketing/` so the Go binary can serve
it at `/` (apex).

## Stack

- **React 19** + **TypeScript 5**
- **Vite 6** for the dev server and production build
- **Tailwind v4** with the `@tailwindcss/vite` plugin
- **react-router-dom 7** for client-side routing
- **react-helmet-async** for per-page SEO
- **lucide-react** for icons
- **vitest 4** + **@testing-library/react** for unit tests

## Scripts

| Command | What it does |
| ------- | ------------ |
| `npm run dev` | Start the dev server on http://localhost:3002 |
| `npm run build` | Build the production bundle to `dist/` |
| `npm run preview` | Serve the production build locally |
| `npm run test` | Run the vitest unit-test suite |
| `npm run test:watch` | Run the vitest suite in watch mode |
| `npm run build:sitemap` | Generate `dist/sitemap.xml` from the route table |
| `npm run check:links` | Assert every internal `<Link to>` and `href` resolves to a known route |
| `npm run check:seo` | Assert `dist/index.html` has the required SEO meta tags |

## Source layout

```
web/marketing/
├── index.html
├── package.json
├── vite.config.ts
├── tsconfig.json
├── public/                 # static assets (favicon, robots.txt)
├── scripts/
│   ├── build-sitemap.mjs
│   ├── check-internal-links.mjs
│   └── check-seo-tags.mjs
├── src/
│   ├── main.tsx            # entry — BrowserRouter + HelmetProvider
│   ├── styles/             # design tokens, base, components, pages
│   ├── lib/                # plans, seo, i18n, route-table, design-tokens
│   ├── data/               # blog-posts, docs-index, features-matrix
│   ├── components/         # 17 shared components
│   └── pages/              # 19 page components
└── tests/
    └── unit/               # vitest specs
```

## Routes

| Path | Page | Source of truth |
| ---- | ---- | --------------- |
| `/` | Home | §2 of the spec |
| `/pricing` | Pricing | §0.4 + §2.6 |
| `/features` | Features | §2.3 |
| `/enterprise` | Enterprise | §2.4 |
| `/security` | Security & compliance | §7 + §8 |
| `/docs` | Documentation index | §1 + `docs/customer/` |
| `/api` | API one-pager | §0.1 + §2.5 |
| `/status` | Status | §1 + `docs/customer/status.md` |
| `/about` | About | §2.10 |
| `/contact` | Contact form (mailto fallback) | §2.11 |
| `/blog` | Blog index (honest empty state) | §1 |
| `/blog/welcome-to-orvix` | Welcome post | internal |
| `/legal` | Legal index | §1 |
| `/legal/terms` | Terms of service | §1 |
| `/legal/privacy` | Privacy policy | §1 |
| `/legal/aup` | Acceptable use policy | §1 |
| `/legal/cookies` | Cookie policy | §1 |
| `/legal/data-and-privacy` | Data and privacy | §1 |
| `*` | 404 | — |

## Pricing

Pinned to the Launch Specification v1.0 §0.4 and the Go billing seed
(`internal/billing/service.go SeedDefaultPlans()`). The unit test
`tests/unit/plans-truth.test.ts` asserts every value against the spec
text so a stray edit fails the build.

| Plan | USD/mo | USD/yr | Domains | Mailboxes | Storage | Sends/day |
| ---- | ------ | ------ | ------- | --------- | ------- | --------- |
| Free | $0 | $0 | 1 | 5 | 1 GB | 500 |
| Starter | $9.99 | $99.90 | 3 | 25 | 10 GB | 2,000 |
| Business | $29.99 | $299.90 | 10 | 100 | 100 GB | 10,000 |
| Enterprise | $99.99 | $999.90 | 100 | 1,000 | 1 TB | 100,000 |

Annual prices are 10× monthly (a 16% discount), per the spec. There is
no "Pro" tier and no invented features.

## Screenshots

The product surfaces are rendered as labelled SVG illustrations
(`src/components/Illustration.tsx`). Each illustration carries a small
"Illustration" badge in the corner so it cannot be confused with a real
screenshot.

Real product screenshots were not captured for v1.0 because the
Orvix server binary is a Linux ELF and the development environment
here is Windows. When the site is built on a Linux host, the
`scripts/capture-screens.mjs` script (Playwright + a running Orvix
server) can produce real captures and the page components should
swap to `<img>` tags pointing at `src/assets/screens/`.

## Accessibility

- Semantic HTML5 elements (`<header>`, `<main>`, `<footer>`, `<nav>`,
  `<article>`, `<section>`, `<aside>`).
- Skip-to-main-content link.
- Visible focus rings on every interactive element.
- All decorative SVGs marked `aria-hidden="true"`.
- `prefers-reduced-motion` honored.
- Color contrast: text on surface tokens meets WCAG 2.1 AA for body
  text (≥ 4.5:1) and large text (≥ 3:1).
- Keyboard: mobile menu, FAQ accordions, comparison table, and the
  pricing toggle are all reachable and operable with the keyboard
  alone.

## Release policy

This site follows the same release policy as `web/webmail` and
`web/admin`: the build output is committed to `release/marketing/`.
The Go binary already serves `/admin` and `/webmail` from
`release/admin/` and `release/webmail/`; the marketing SPA is
expected to be served from `/` in the same way. The backend
change that wires the new path is intentionally out of scope for
this PR.

## CI checks

The `npm test` and `npm run build` scripts both run in CI. The
`check-internal-links.mjs` and `check-seo-tags.mjs` scripts are
recommended to be added to the marketing CI pipeline so a broken
internal link or missing meta tag fails the build.
