import type { SettingsSectionSchema } from "./contract";

// Hand-built from AdminSettingsGet's exact live-config field set
// (internal/api/handlers/admin_settings.go) — not derived generically
// from response keys, so a field can never appear with the wrong type
// or as editable when the backend only ever reports it informationally.
export const SETTINGS_SCHEMA: SettingsSectionSchema[] = [
  {
    section: "general",
    label: "General",
    fields: [
      { key: "primary_domain", label: "Primary domain", type: "string" },
      { key: "public_ipv4", label: "Public IPv4", type: "string" },
      { key: "public_ipv6", label: "Public IPv6", type: "string" },
      { key: "hostname", label: "Hostname", type: "string" },
    ],
  },
  {
    section: "mail_listeners",
    label: "Mail Listeners",
    fields: [
      { key: "smtp_host", label: "SMTP host", type: "string" },
      { key: "smtp_port", label: "SMTP port", type: "number" },
      { key: "imap_host", label: "IMAP host", type: "string" },
      { key: "imap_port", label: "IMAP port", type: "number" },
      { key: "pop3_host", label: "POP3 host", type: "string" },
      { key: "pop3_port", label: "POP3 port", type: "number" },
      { key: "jmap_host", label: "JMAP host", type: "string" },
      { key: "jmap_port", label: "JMAP port", type: "number" },
      { key: "submission_enabled", label: "Submission enabled", type: "boolean" },
      { key: "submission_host", label: "Submission host", type: "string" },
      { key: "submission_port", label: "Submission port", type: "number" },
      { key: "smtps_enabled", label: "SMTPS enabled", type: "boolean" },
      { key: "smtps_host", label: "SMTPS host", type: "string" },
      { key: "smtps_port", label: "SMTPS port", type: "number" },
      { key: "imaps_enabled", label: "IMAPS enabled", type: "boolean" },
      { key: "imaps_host", label: "IMAPS host", type: "string" },
      { key: "imaps_port", label: "IMAPS port", type: "number" },
      { key: "pop3s_enabled", label: "POP3S enabled", type: "boolean" },
      { key: "pop3s_host", label: "POP3S host", type: "string" },
      { key: "pop3s_port", label: "POP3S port", type: "number" },
    ],
  },
  {
    section: "security",
    label: "Security",
    fields: [
      { key: "password_min_len", label: "Minimum password length", type: "number" },
      { key: "session_ttl_seconds", label: "Session TTL (seconds)", type: "number" },
      { key: "refresh_ttl_seconds", label: "Refresh token TTL (seconds)", type: "number" },
    ],
  },
  {
    section: "backup",
    label: "Backup",
    fields: [
      { key: "dir", label: "Backup directory", type: "string" },
      { key: "retention_count", label: "Retention count", type: "number" },
    ],
  },
  {
    section: "dns",
    label: "DNS",
    fields: [
      { key: "public_ipv4", label: "Public IPv4", type: "string" },
      { key: "public_ipv6", label: "Public IPv6", type: "string" },
      { key: "cloudflare_zone_configured", label: "Cloudflare zone configured", type: "boolean", readOnly: true },
      { key: "namecheap_configured", label: "Namecheap configured", type: "boolean", readOnly: true },
    ],
  },
];

// The "build" section (version/commit/tag/build_time/channel/
// go_version/os/arch/is_dev_build) is purely informational — rendered
// separately, never as editable fields.
export const BUILD_INFO_FIELDS: { key: string; label: string }[] = [
  { key: "version", label: "Version" },
  { key: "commit", label: "Commit" },
  { key: "tag", label: "Tag" },
  { key: "build_time", label: "Build time" },
  { key: "channel", label: "Channel" },
  { key: "go_version", label: "Go version" },
  { key: "os", label: "OS" },
  { key: "arch", label: "Arch" },
];
