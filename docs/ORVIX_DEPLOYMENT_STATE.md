# Orvix Deployment State

## Current State Summary

| Property | Value |
|----------|-------|
| Authoritative repository ref | `origin/main` |
| Last state-sync merge | `bd91d15a8fde4cdddc14b94aaf7c738ba6616a31` (PR #37) |
| Last security baseline | `e7f5441c25732b7583f3e57053f1e4d79f417fc5` (PR #27) |
| Latest release candidate commit | NOT VERIFIED |
| Production deployed commit | NOT VERIFIED |
| Staging deployed commit | NOT VERIFIED |
| Deployment date (latest) | NOT VERIFIED |
| Backup status | NOT VERIFIED |
| Rollback reference | NOT VERIFIED |
| Migration status | NOT VERIFIED |
| Deployment approval status | NOT GRANTED |

## Deployment Status of Merged PRs

| PR | Commit | Deployed |
|----|--------|----------|
| #27 (security baseline) | `e7f5441...` | NO |
| #37 (state-sync) | `bd91d15...` | NO |

Neither PR #27 nor PR #37 has been deployed to any environment. Both exist
only on the GitHub `main` branch.

## Deployment History

| Date | SHA | Version | Target | Approved |
|------|-----|---------|--------|----------|
| NOT VERIFIED | NOT VERIFIED | NOT VERIFIED | NOT VERIFIED | NOT VERIFIED |

## Pending Deployment

| SHA | Version | Target | Gate |
|-----|---------|--------|------|
| `origin/main` | Not yet tagged | Staging (first) | Staging acceptance |
| `origin/main` | Not yet tagged | Production | Closed beta + approval |

## Notes

- No deployment may occur without backup and explicit rollback approval.
- Staging deployment must precede any production deployment.
- Closed beta must complete before production deployment.
- This document must be updated on every deployment.
