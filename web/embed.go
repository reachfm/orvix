package web

import "embed"

//go:embed admin/dist
//go:embed webmail/dist
//go:embed portal/dist
var FrontendFS embed.FS
