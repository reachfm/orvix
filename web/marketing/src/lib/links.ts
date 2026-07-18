export const CANONICAL = {
  marketing: "https://orvix.email",
  portal: "https://admin.orvix.email/admin",
  webmail: "https://webmail.orvix.email/webmail",
  docs: "https://docs.orvix.email",
  api: "https://orvix.email/api/v1",
} as const;

export const PORTAL_BASE = CANONICAL.portal;
export const PORTAL_LOGIN = `${CANONICAL.portal}/login`;
export const PORTAL_SIGNUP = `${CANONICAL.portal}/signup`;
export const DOCS_BASE = CANONICAL.docs;
