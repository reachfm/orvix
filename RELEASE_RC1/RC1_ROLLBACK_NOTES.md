# RC1 Rollback Notes

Audited commit:

`61fa05595c134ae3fe333f438e6989ca08ffefc4`

Rollback principles:
- Do not create a release tag as part of rollback.
- Do not version bump during rollback.
- Do not publish replacement assets without a separate release procedure.
- Preserve logs, reports, and certification outputs before rollback.

Rollback triggers:
- Backend gate failure after freeze.
- Race gate failure after freeze.
- Frontend gate failure after freeze.
- Integrated harness failure after freeze.
- Security regression or data-loss risk discovered after freeze.

Rollback approach:
1. Stop rollout.
2. Preserve the failing command output.
3. Preserve runtime logs and deployment state.
4. Restore the last known certified deployment artifact or workspace snapshot.
5. Rerun:
   - `go build ./...`
   - `go test ./...`
   - `go vet ./...`
   - `go test -race ./...`
   - Admin frontend gates
   - Webmail frontend gates
   - Integrated RC1 harness
6. Generate a new post-rollback certification report before any renewed rollout.

Freeze rule:

No new features after RC1 freeze.
