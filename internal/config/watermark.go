package config

import (
	"fmt"
	"runtime"
	"time"
)

// Watermark contains embedded copyright and build information.
type Watermark struct {
	Product    string `json:"product"`
	Version    string `json:"version"`
	Copyright  string `json:"copyright"`
	BuildTime  string `json:"build_time"`
	GoVersion  string `json:"go_version"`
	Arch       string `json:"arch"`
	License    string `json:"license"`
}

var (
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
func GetWatermark() Watermark {
	return Watermark{
		Product:   "Orvix Email Server Platform",
		Version:   buildVersion,
		Copyright: fmt.Sprintf("Copyright © %d Orvix. All rights reserved.", time.Now().Year()),
		BuildTime: buildTime,
		GoVersion: runtime.Version(),
		Arch:      runtime.GOOS + "/" + runtime.GOARCH,
		License:   "Proprietary - License Required",
	}
}

// GetBuildCommit returns the build commit SHA, or "not reported"
// when the binary was not linked with one. Never returns an empty
// string; the dashboard surfaces this verbatim.
func GetBuildCommit() string {
	if buildCommit == "" {
		return "not reported"
	}
	return buildCommit
}

// CanaryToken returns an embedded canary string for watermarking.
func CanaryToken() string {
	return fmt.Sprintf("ORVIX-%s-%s-%s", buildVersion, buildTime, runtime.GOARCH)
}
