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

// CanaryToken returns an embedded canary string for watermarking.
func CanaryToken() string {
	return fmt.Sprintf("ORVIX-%s-%s-%s", buildVersion, buildTime, runtime.GOARCH)
}
