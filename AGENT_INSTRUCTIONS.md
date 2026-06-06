# Orvix — Autonomous Agent Instructions

You are a senior Go and React engineer.
Your ONLY job is to build the Orvix project completely.
You work 100% autonomously — no stopping, no questions, no waiting.

---

## PRIME DIRECTIVE

**NEVER stop. NEVER ask. NEVER wait.**

- Face a decision → make the best technical choice and continue
- Face an error → fix it and continue
- Face ambiguity → use MVP.md decisions and continue
- Stop ONLY when every [ ] in MVP.md Build Order is [x]

---

## EVERY SESSION — DO THIS EXACTLY

```
Step 1: Read MVP.md completely
Step 2: Read PROGRESS.md (create if missing)
Step 3: Find first unchecked [ ] task in Build Order
Step 4: Execute that task completely
Step 5: Mark it [x] in MVP.md
Step 6: Update PROGRESS.md
Step 7: Go to next task — do not pause
```

---

## PROGRESS.md — KEEP THIS UPDATED

```markdown
# Build Progress

## Last Updated: 2024-01-01 00:00:00
## Current Phase: Phase 1
## Current Task: Project structure setup
## Done: 0 / 47 tasks

## Completed
- [x] example task

## Blocked (skip and return later)
- none

## Notes
- none
```

---

## ABSOLUTE RULES

### Rule 1 — Never Ask
```
❌ "Should I use X or Y?"
❌ "What do you want next?"
❌ "Do you want me to continue?"
✅ Decide. Execute. Continue.
```

### Rule 2 — Never Stop on Error
```
❌ Report error and wait
✅ Fix the error
✅ If fix fails → try alternative
✅ If all fail → log in PROGRESS.md blockers, skip, continue
✅ Return to blocked tasks at the end
```

### Rule 3 — Verify After Every Go Task
```
go build ./...
→ if fails: fix before next task
→ if passes: mark [x] and continue
```

### Rule 4 — Verify After Every Frontend Task
```
npm run build
→ if fails: fix before next task
→ if passes: mark [x] and continue
```

### Rule 5 — Migrations Are Additive Only
```
✅ ADD columns, tables, indexes
❌ NEVER DROP, RENAME, or MODIFY existing migrations
✅ New change = new migration file
✅ Format: 001_initial.sql, 002_add_users.sql
```

### Rule 6 — Module Independence
```
✅ Each module in internal/[module]/ is self-contained
✅ Modules communicate via interfaces only
✅ No direct imports between modules
✅ Core never imports modules
✅ All modules implement the Module interface from MVP.md
```

### Rule 7 — Stalwart Integration
```
✅ Never modify Stalwart source code
✅ Talk to Stalwart ONLY via its REST API
✅ Listen to Stalwart events via webhooks
✅ Stalwart binary is embedded in orvix binary
✅ Orvix starts Stalwart as a managed subprocess
```

### Rule 8 — Production Quality Always
```
✅ Full error handling on every function
✅ Zap structured logging on every operation
✅ Context cancellation support everywhere
✅ No TODO comments — implement or skip with PROGRESS.md note
✅ Every exported function has a Go doc comment
```

---

## DEFAULT DECISIONS

| Decision | Default |
|----------|---------|
| Error handling | return error + zap.Error log |
| DB query timeout | 30 seconds |
| HTTP timeout | 60 seconds |
| SMTP timeout | 5 minutes |
| Retry attempts | 3x exponential backoff |
| SMTP ports | 25, 465, 587 |
| IMAP ports | 143, 993 |
| POP3 ports | 110, 995 |
| HTTP ports | 80, 443 |
| Admin port | 8080 |
| JWT access TTL | 15 minutes |
| JWT refresh TTL | 30 days |
| Max email size | 50MB |
| Max attachment | 25MB |
| Password min | 12 characters |
| Login rate limit | 5 attempts / 15 min |
| Queue retry | 5m, 30m, 2h, 6h, 24h |
| Config path | /etc/orvix/orvix.yaml |
| Data path | /var/lib/orvix |
| Log path | /var/log/orvix |
| Stalwart API | http://localhost:18080 |
| Stalwart data | /var/lib/orvix/stalwart |

---

## WHEN ALL TASKS COMPLETE

1. Run: `go build ./...`
2. Run: `go test ./...`
3. Run: `npm run build` in web/webmail and web/admin
4. Write final PROGRESS.md summary
5. Write HANDOFF.md:
   - How to run
   - Environment variables
   - How to test
   - Known issues
   - What needs human review

---

## START NOW

Read MVP.md → find first [ ] → execute → never stop.
