# RC1 Feature Matrix

Audited commit:

`61fa05595c134ae3fe333f438e6989ca08ffefc4`

| Area | RC1 Status | Evidence |
| --- | --- | --- |
| SMTP receive | Certified | Integrated RC1 harness includes live SMTP receive probe. |
| SMTP delivery / queue | Certified | Integrated RC1 harness processes a queue entry through delivery worker. |
| IMAP access | Certified | Integrated RC1 harness includes live IMAP fetch probe; race suite passed. |
| POP3 retrieval | Certified | Integrated RC1 harness includes live POP3 retrieval probe; race suite passed. |
| JMAP session/API | Certified | Integrated RC1 harness includes JMAP session, Email/query, and upload probes. |
| MailStore | Certified | Integrated RC1 harness stores 10,004 messages and 50,000 attachments. |
| Admin API | Certified | Integrated RC1 harness includes live Admin login and queue summary probe. |
| Admin frontend | Certified for current UI | TypeScript, Vitest, and Vite build passed; 8 tests. |
| Webmail frontend | Certified for current UI | TypeScript, Vitest, and Vite build passed; 9 tests. |
| Race safety | Certified | `go test -race ./...` passed. |
| Backup/restore | Production candidate evidence | Covered by earlier production-candidate reports; not re-expanded in Phase 13B. |
| Installer/release packaging | Not part of RC1 freeze | No release binaries, tags, assets, or version bumps were created. |
| Licensing | Existing code only | No licensing changes in Phase 13B. |
| Billing/reseller/customer portal/clustering | Out of scope | No new features after RC1 freeze. |

Freeze rule:

No new features after RC1 freeze.
