package license

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
)

// HardwareFingerprint collects unique hardware identifiers.
type HardwareFingerprint struct {
	Hostname   string `json:"hostname"`
	MAC        string `json:"mac"`
	CPUCores   int    `json:"cpu_cores"`
	OS         string `json:"os"`
	MachineID  string `json:"machine_id"`
}

// CollectFingerprint gathers hardware information for license binding.
func CollectFingerprint() (*HardwareFingerprint, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to get hostname: %w", err)
	}

	fp := &HardwareFingerprint{
		Hostname: hostname,
		CPUCores: runtime.NumCPU(),
		OS:       runtime.GOOS + "/" + runtime.GOARCH,
	}

	mac, err := getMACAddress()
	if err == nil {
		fp.MAC = mac
	}

	if id, err := getMachineID(); err == nil {
		fp.MachineID = id
	}

	return fp, nil
}

// Hash returns a SHA-256 fingerprint hash.
func (fp *HardwareFingerprint) Hash() string {
	data := fmt.Sprintf("%s|%s|%d|%s|%s",
		fp.Hostname, fp.MAC, fp.CPUCores, fp.OS, fp.MachineID)
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func getMACAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && len(iface.HardwareAddr) > 0 {
			return iface.HardwareAddr.String(), nil
		}
	}
	return "", fmt.Errorf("no active network interface found")
}

func getMachineID() (string, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data)), nil
	}
	data, err = os.ReadFile("/var/lib/dbus/machine-id")
	if err == nil && len(data) > 0 {
		return strings.TrimSpace(string(data)), nil
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
