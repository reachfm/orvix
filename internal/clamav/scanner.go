package clamav

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Scanner performs virus scanning via ClamAV daemon.
type Scanner struct {
	host   string
	port   int
	timeout time.Duration
	logger *zap.Logger
}

// ScanResult represents a ClamAV scan result.
type ScanResult struct {
	Filename string `json:"filename"`
	Infected bool   `json:"infected"`
	Virus    string `json:"virus,omitempty"`
	Size     int64  `json:"size"`
	Duration int64  `json:"duration_ms"`
}

// NewScanner creates a new ClamAV scanner.
func NewScanner(host string, port int, logger *zap.Logger) *Scanner {
	return &Scanner{
		host:    host,
		port:    port,
		timeout: 30 * time.Second,
		logger:  logger,
	}
}

// Ping checks if the ClamAV daemon is reachable.
func (s *Scanner) Ping(ctx context.Context) error {
	addr := net.JoinHostPort(s.host, fmt.Sprintf("%d", s.port))
	conn, err := net.DialTimeout("tcp", addr, s.timeout)
	if err != nil {
		return fmt.Errorf("clamav not reachable: %w", err)
	}
	defer conn.Close()
	return nil
}

// ScanBytes scans a byte slice for viruses via ClamAV INSTREAM.
func (s *Scanner) ScanBytes(ctx context.Context, data []byte, filename string) (*ScanResult, error) {
	start := time.Now()

	addr := net.JoinHostPort(s.host, fmt.Sprintf("%d", s.port))
	conn, err := net.DialTimeout("tcp", addr, s.timeout)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to clamav: %w", err)
	}
	defer conn.Close()

	// Send INSTREAM command
	conn.Write([]byte("zINSTREAM\000"))

	// Send data in chunks
	buf := make([]byte, 8192)
	offset := 0
	for offset < len(data) {
		chunkSize := len(data) - offset
		if chunkSize > len(buf) {
			chunkSize = len(buf)
		}
		copy(buf[:4], intToBytes(chunkSize))
		copy(buf[4:], data[offset:offset+chunkSize])
		conn.Write(buf[:4+chunkSize])
		offset += chunkSize
	}

	// Send zero-length chunk to signal end
	conn.Write([]byte{0, 0, 0, 0})

	// Read response
	resp, _ := bufio.NewReader(conn).ReadString('\000')
	result := &ScanResult{
		Filename: filename,
		Size:     int64(len(data)),
		Duration: time.Since(start).Milliseconds(),
	}

	if strings.Contains(resp, "FOUND") {
		result.Infected = true
		parts := strings.Split(resp, ": ")
		if len(parts) > 2 {
			result.Virus = strings.TrimSpace(parts[2])
		}
	}

	s.logger.Info("clamav scan complete",
		zap.String("filename", filename),
		zap.Bool("infected", result.Infected),
		zap.Int64("duration_ms", result.Duration),
	)

	return result, nil
}

func intToBytes(n int) []byte {
	return []byte{
		byte(n >> 24),
		byte(n >> 16),
		byte(n >> 8),
		byte(n),
	}
}
