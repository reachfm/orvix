# RC1 Deployment Notes

Audited commit:

`61fa05595c134ae3fe333f438e6989ca08ffefc4`

Deployment posture:
- Status: RC1 FROZEN
- Production candidate status: PRODUCTION CANDIDATE
- Release artifacts: not created
- Tags: not created
- Version bump: not performed

Pre-deployment verification required:
1. Confirm repository state matches the audited commit and approved RC1 artifacts.
2. Run backend gates:
   - `go build ./...`
   - `go test ./...`
   - `go vet ./...`
   - `go test -race ./...`
3. Run frontend gates:
   - `web/admin`: `npx.cmd tsc --noEmit`, `npx.cmd vitest run`, `npx.cmd vite build`
   - `web/webmail`: `npx.cmd tsc --noEmit`, `npx.cmd vitest run`, `npx.cmd vite build`
4. Run integrated RC1 harness when certifying deployment readiness:
   - `$env:ORVIX_RC1_INTEGRATED='1'; go test ./internal/coremail/jmap -run TestRC1IntegratedSystemCertification -count=1 -v`

Windows race-gate environment:
```powershell
$gccRoot='C:\Users\Mostafa\AppData\Local\Temp\orvix-w64devkit\w64devkit'
$gccBin=Join-Path $gccRoot 'bin'
$gcc=Join-Path $gccBin 'gcc.exe'
$env:Path = "$gccBin;$env:Path"
$env:PATH=$env:Path
$env:CGO_ENABLED='1'
$env:CC=$gcc
$env:COMPILER_PATH=$gccBin
$env:LIBRARY_PATH=(Join-Path $gccRoot 'lib')
go test -race ./...
```

Freeze rule:

No new features after RC1 freeze.
