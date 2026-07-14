package config

import (
	"fmt"
	"runtime"
	"time"

	"github.com/orvix/orvix/internal/buildinfo"
)

// Watermark contains embedded copyright and build information.
//
// Watermark now sources build metadata from internal/buildinfo so the
// CLI, runtime telemetry, and admin settings endpoint all read the
// same values. The buildVersion / buildTime / buildCommit package
// variables are retained for backward compatibility with any tooling
// that still imports them directly; they are kept in sync with the
// buildinfo values when not overridden via -ldflags at link time.
type Watermark struct {
	Product   string `json:"product"`
	Version   string `json:"version"`
	Copyright string `json:"copyright"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	Arch      string `json:"arch"`
	License   string `json:"license"`
}

var (
	// These vars remain for backward compatibility. New code should
	// read from internal/buildinfo.Get() instead.
	buildVersion = "1.0.0"
	buildTime    = "development"
	// buildCommit is injected at link time via -ldflags:
	//
	//	go build -ldflags "-X github.com/orvix/orvix/internal/config.buildCommit=$(git rev-parse HEAD)"
	//
	// When not injected, the runtime telemetry endpoint surfaces
	// "not reported" rather than fabricating a SHA.
	buildCommit = "not reported"
)

// GetWatermark returns embedded copyright and build information.
// Sources version/commit/build_time from internal/buildinfo.
func GetWatermark() Watermark {
	bi := buildinfo.Get()
	return Watermark{
		Product:   "Orvix Email Server Platform",
		Version:   bi.Version,
		Copyright: fmt.Sprintf("Copyright © %d Orvix. All rights reserved.", time.Now().Year()),
		BuildTime: bi.BuildTime,
		GoVersion: bi.GoVersion,
		Arch:      bi.OS + "/" + bi.Arch,
		License:   "Proprietary - License Required",
	}
}

// GetBuildCommit returns the build commit SHA, or "not reported"
// when the binary was not linked with one. Never returns an empty
// string; the dashboard surfaces this verbatim.
func GetBuildCommit() string {
	bi := buildinfo.Get()
	if bi.Commit == "" {
		return "not reported"
	}
	return bi.Commit
}

// CanaryToken returns an embedded canary string for watermarking.
func CanaryToken() string {
	bi := buildinfo.Get()
	return fmt.Sprintf("ORVIX-%s-%s-%s", bi.Version, bi.BuildTime, bi.Arch)
}

// runtimeGoVersion is a small helper kept here for callers that
// only need the Go runtime version without the rest of buildinfo.
func runtimeGoVersion() string { return runtime.Version() }
