package firewall

import (
	"net"
	"path/filepath"
	"strings"
)

var dangerousExtensions = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true,
	".ps1": true, ".vbs": true, ".js": true, ".jar": true,
	".scr": true, ".pif": true, ".vbe": true, ".msi": true,
}

var suspiciousTLDs = map[string]bool{
	".xyz": true, ".top": true, ".work": true, ".date": true,
	".loan": true, ".download": true, ".men": true, ".party": true,
	".gq": true, ".ml": true, ".cf": true, ".tk": true,
}

func (e *Engine) connectionFilter(conn *Connection) (Action, error) {
	if e.reputation != nil {
		score, _ := e.reputation.CheckIP(conn.IP)
		if score > 80 {
			return ActionBlock, nil
		}
	}

	if e.geoBlock != nil {
		country := resolveCountry(conn.IP)
		if country != "" && e.geoBlock.IsBlocked(country) {
			return ActionBlock, nil
		}
	}

	if !isPrivateIP(conn.IP) {
		ip := net.ParseIP(conn.IP)
		if ip == nil {
			return ActionBlock, nil
		}
	}

	return ActionPass, nil
}

func (e *Engine) protocolFilter(conn *Connection) (Action, error) {
	if conn.EHLO == "" {
		return ActionBlock, nil
	}

	if len(conn.EHLO) < 3 || len(conn.EHLO) > 255 {
		return ActionBlock, nil
	}

	if strings.Contains(conn.EHLO, "\n") || strings.Contains(conn.EHLO, "\r") {
		return ActionBlock, nil
	}

	return ActionPass, nil
}

func (e *Engine) authFilter(conn *Connection) (Action, error) {
	if conn.SPFResult == "fail" || conn.SPFResult == "hardfail" {
		return ActionBlock, nil
	}

	if conn.DKIMResult == "fail" {
		return ActionQuarantine, nil
	}

	if conn.DMARCResult == "reject" || conn.DMARCResult == "quarantine" {
		return ActionBlock, nil
	}

	return ActionPass, nil
}

func (e *Engine) contentFilter(conn *Connection) (Action, error) {
	for _, att := range conn.Attachments {
		ext := strings.ToLower(filepath.Ext(att.Filename))
		if dangerousExtensions[ext] {
			return ActionBlock, nil
		}

		if att.Size > 50*1024*1024 {
			return ActionBlock, nil
		}
	}

	if conn.SpamScore > 8.0 {
		return ActionBlock, nil
	}
	if conn.SpamScore > 5.0 {
		return ActionQuarantine, nil
	}

	return ActionPass, nil
}

func (e *Engine) behavioralFilter(conn *Connection) (Action, error) {
	if conn.MsgCount24h > 1000 {
		return ActionThrottle, nil
	}

	if conn.MsgCount24h > 5000 {
		return ActionBlock, nil
	}

	for _, rcpt := range conn.RcptTo {
		parts := strings.Split(rcpt, "@")
		if len(parts) == 2 {
			domain := parts[1]
			domainParts := strings.Split(domain, ".")
			if len(domainParts) >= 2 {
				tld := "." + domainParts[len(domainParts)-1]
				if suspiciousTLDs[tld] {
					return ActionQuarantine, nil
				}
			}
		}
	}

	return ActionPass, nil
}

func resolveCountry(ip string) string {
	return ""
}

func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	privateRanges := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8"}
	for _, cidr := range privateRanges {
		_, network, _ := net.ParseCIDR(cidr)
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
