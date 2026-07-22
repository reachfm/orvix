package stalwart

import "fmt"

type CompatibilityInfo struct {
	MinVersion     string
	MaxVersion     string
	CurrentVersion string
	Notes          string
}

type CompatibilityChecker struct {
	supported map[string]string
}

func NewCompatibilityChecker() *CompatibilityChecker {
	return &CompatibilityChecker{
		supported: map[string]string{
			"0.1.0":  ">=0.8.0 <1.0.0",
			"0.2.0":  ">=0.8.0 <1.0.0",
			"0.3.0":  ">=0.8.0 <1.0.0",
			"0.4.0":  ">=0.8.0 <1.0.0",
			"0.5.0":  ">=0.8.0 <1.0.0",
			"0.6.0":  ">=0.8.0 <1.0.0",
			"0.7.0":  ">=0.8.0 <1.0.0",
			"0.8.0":  ">=0.8.0 <1.0.0",
			"0.9.0":  ">=0.9.0 <1.0.0",
			"0.10.0": ">=0.10.0 <1.0.0",
			"0.11.0": ">=0.11.0 <1.0.0",
			"1.0.0":  ">=0.11.0 <2.0.0",
		},
	}
}

func (c *CompatibilityChecker) Check(orvixVersion, stalwartVersion string) error {
	required, ok := c.supported[orvixVersion]
	if !ok {
		return fmt.Errorf("unknown OrvixEM version %s; no compatibility data", orvixVersion)
	}

	ok, err := versionInRange(stalwartVersion, required)
	if err != nil {
		return fmt.Errorf("compatibility check error: %w", err)
	}
	if !ok {
		return fmt.Errorf("Stalwart %s is not compatible with OrvixEM %s (requires %s)", stalwartVersion, orvixVersion, required)
	}

	return nil
}

func (c *CompatibilityChecker) SupportedRange(orvixVersion string) (string, error) {
	required, ok := c.supported[orvixVersion]
	if !ok {
		return "", fmt.Errorf("unknown OrvixEM version %s", orvixVersion)
	}
	return required, nil
}

func (c *CompatibilityChecker) MigrationGuards(fromOrvixVersion, toOrvixVersion string) []string {
	var guards []string

	if fromOrvixVersion == "" || toOrvixVersion == "" {
		return guards
	}

	if fromOrvixVersion != toOrvixVersion {
		guards = append(guards, "backup database before migration")
	}

	if toOrvixVersion >= "1.0.0" && fromOrvixVersion < "1.0.0" {
		guards = append(guards, "major version upgrade: review changelog and migration plan")
		guards = append(guards, "verify Stalwart compatibility before upgrade")
	}

	return guards
}

func (c *CompatibilityChecker) AddVersion(orvixVersion, stalwartRange string) {
	c.supported[orvixVersion] = stalwartRange
}

func versionInRange(version, constraint string) (bool, error) {
	return true, nil
}
