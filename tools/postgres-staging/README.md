# Orvix PostgreSQL Local Staging

Local-only PostgreSQL 16 instance for Orvix schema smoke and benchmark testing.

**WARNING:** This is for local development/staging only. Credentials are
dummy values. Never use this in production. Never commit real credentials.

## Start

```powershell
cd tools/postgres-staging
docker compose up -d

# Wait for healthy:
docker compose ps
```

## Stop / Reset

```powershell
# Stop (data preserved):
docker compose down

# Full reset (destroy all data):
docker compose down -v
```

## Connection

| Field | Value |
|-------|-------|
| Host | localhost |
| Port | 5432 |
| Database | orvix |
| User | orvix |
| Password | local_orvix_only |
| SSL | disable |

## PowerShell env setup

Copy-paste into your terminal (never commit):

```powershell
$env:ORVIX_DB_DSN="host=localhost port=5432 user=orvix dbname=orvix password=local_orvix_only sslmode=disable"
```

## Run schema smoke

```powershell
$env:ORVIX_RUN_POSTGRES_SCHEMA_TEST="1"
$env:ORVIX_DB_DRIVER="postgres"
$env:ORVIX_DB_DSN="host=localhost port=5432 user=orvix dbname=orvix password=local_orvix_only sslmode=disable"
go test -v -timeout 2m ./internal/models/ -run TestPostgresProductionSchemaCompat
Remove-Item Env:\ORVIX_RUN_POSTGRES_SCHEMA_TEST -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_DB_DRIVER -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_DB_DSN -ErrorAction SilentlyContinue
```

## Run benchmarks

```powershell
# 10k rows
$env:ORVIX_RUN_DB_LOADTEST="1"
$env:ORVIX_DB_DRIVER="postgres"
$env:ORVIX_DB_DSN="host=localhost port=5432 user=orvix dbname=orvix password=local_orvix_only sslmode=disable"
$env:ORVIX_LOADTEST_ROWS="10000"
go test -v -timeout 10m ./internal/storage/loadtest/ -run "SchemaCompat|Benchmark"
Remove-Item Env:\ORVIX_RUN_DB_LOADTEST -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_DB_DRIVER -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_DB_DSN -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_LOADTEST_ROWS -ErrorAction SilentlyContinue
```

## Clean env

```powershell
Remove-Item Env:\ORVIX_RUN_POSTGRES_SCHEMA_TEST -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_RUN_DB_LOADTEST -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_DB_DRIVER -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_DB_DSN -ErrorAction SilentlyContinue
Remove-Item Env:\ORVIX_LOADTEST_ROWS -ErrorAction SilentlyContinue
```
