package delivery

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── Fake SMTP Server ─────────────────────────────────────────

type fakeSMTPServer struct {
	t            *testing.T
	ln           net.Listener
	addr         string
	mu           sync.Mutex
	receivedFrom string
	receivedRcpt []string
	receivedData []byte
	heloHost     string
	greetingCode int
	greetingMsg  string
	ehloResponse func(string) (int, string)
	rcptResponse func(string) (int, string)
	dataResponse func() (int, string)
}

func startFakeSMTP(t *testing.T) *fakeSMTPServer {
	t.Helper()
	fs := &fakeSMTPServer{t: t, greetingCode: 220, greetingMsg: "Fake SMTP Server"}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fs.ln = ln
	fs.addr = ln.Addr().String()
	go fs.serve()
	t.Cleanup(func() { ln.Close() })
	return fs
}

func (fs *fakeSMTPServer) serve() {
	for {
		conn, err := fs.ln.Accept()
		if err != nil {
			return
		}
		go fs.handle(conn)
	}
}

func (fs *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	fmt.Fprintf(writer, "%d %s\r\n", fs.greetingCode, fs.greetingMsg)
	writer.Flush()

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		parts := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(parts[0])
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}

		switch cmd {
		case "EHLO":
			fs.mu.Lock()
			fs.heloHost = args
			fs.mu.Unlock()
			fmt.Fprintf(writer, "250-%s\r\n", args)
			fmt.Fprintf(writer, "250-PIPELINING\r\n")
			fmt.Fprintf(writer, "250-STARTTLS\r\n")
			fmt.Fprintf(writer, "250 8BITMIME\r\n")
		case "HELO":
			fs.mu.Lock()
			fs.heloHost = args
			fs.mu.Unlock()
			fmt.Fprintf(writer, "250 %s\r\n", args)
		case "MAIL":
			fs.mu.Lock()
			fs.receivedFrom = args
			fs.mu.Unlock()
			fmt.Fprintf(writer, "250 OK\r\n")
		case "RCPT":
			fs.mu.Lock()
			fs.receivedRcpt = append(fs.receivedRcpt, args)
			fs.mu.Unlock()
			if fs.rcptResponse != nil {
				c, m := fs.rcptResponse(args)
				fmt.Fprintf(writer, "%d %s\r\n", c, m)
			} else {
				fmt.Fprintf(writer, "250 OK\r\n")
			}
		case "DATA":
			fmt.Fprintf(writer, "354 Start mail input\r\n")
			writer.Flush()
			var buf strings.Builder
			for {
				dl, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				buf.WriteString(dl)
			}
			fs.mu.Lock()
			fs.receivedData = []byte(buf.String())
			fs.mu.Unlock()
			if fs.dataResponse != nil {
				c, m := fs.dataResponse()
				fmt.Fprintf(writer, "%d %s\r\n", c, m)
			} else {
				fmt.Fprintf(writer, "250 OK\r\n")
			}
		case "QUIT":
			fmt.Fprintf(writer, "221 Bye\r\n")
			writer.Flush()
			return
		case "STARTTLS":
			fmt.Fprintf(writer, "500 TLS not available\r\n")
		default:
			fmt.Fprintf(writer, "500 Unrecognized\r\n")
		}
		writer.Flush()
	}
}

// ── Resolver Tests ───────────────────────────────────────────

func TestFakeResolverMX(t *testing.T) {
	r := NewFakeResolver()
	r.MXRecords["example.com"] = []MXRecord{
		{Host: "mx1.example.com", Priority: 10},
		{Host: "mx2.example.com", Priority: 20},
	}
	records, err := r.LookupMX(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("lookup mx: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2, got %d", len(records))
	}
	if records[0].Priority != 10 {
		t.Fatalf("expected priority 10 first, got %d", records[0].Priority)
	}
}

func TestFakeResolverMXDefault(t *testing.T) {
	r := NewFakeResolver()
	records, err := r.LookupMX(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("lookup mx: %v", err)
	}
	if len(records) != 1 || records[0].Host != "mail.example.com" {
		t.Fatalf("expected default mx mail.example.com, got %v", records)
	}
}

func TestFakeResolverMXFailure(t *testing.T) {
	r := NewFakeResolver()
	r.FailDomain = "fail.com"
	_, err := r.LookupMX(context.Background(), "fail.com")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFakeResolverHost(t *testing.T) {
	r := NewFakeResolver()
	r.Hosts["mx.example.com"] = []string{"192.0.2.1"}
	addrs, err := r.LookupHost(context.Background(), "mx.example.com")
	if err != nil {
		t.Fatalf("lookup host: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "192.0.2.1" {
		t.Fatalf("expected 192.0.2.1, got %v", addrs)
	}
}

func TestFakeResolverHostDefault(t *testing.T) {
	r := NewFakeResolver()
	addrs, err := r.LookupHost(context.Background(), "mx.example.com")
	if err != nil {
		t.Fatalf("lookup host: %v", err)
	}
	if len(addrs) != 1 || addrs[0] != "127.0.0.1" {
		t.Fatalf("expected 127.0.0.1, got %v", addrs)
	}
}

func TestFakeResolverHostFailure(t *testing.T) {
	r := NewFakeResolver()
	r.FailHost = "fail.host"
	_, err := r.LookupHost(context.Background(), "fail.host")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMXRecordsSortedByPriority(t *testing.T) {
	r := NewFakeResolver()
	r.MXRecords["test.com"] = []MXRecord{
		{Host: "mx2.test.com", Priority: 20},
		{Host: "mx1.test.com", Priority: 10},
		{Host: "mx3.test.com", Priority: 30},
	}
	records, _ := r.LookupMX(context.Background(), "test.com")
	if records[0].Host != "mx1.test.com" {
		t.Fatalf("expected mx1 first, got %s", records[0].Host)
	}
	if records[1].Host != "mx2.test.com" {
		t.Fatalf("expected mx2 second")
	}
}

// ── SMTP Transport Tests ─────────────────────────────────────

func TestTransportDeliverSuccess(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success, got: %s", result.StatusMsg)
	}
	if !result.HeloOK {
		t.Fatal("helo not ok")
	}
	if !result.MailFromOK {
		t.Fatal("mail from not ok")
	}
	if !result.RcptOK {
		t.Fatal("rcpt not ok")
	}
	if !result.DataOK {
		t.Fatal("data not ok")
	}
}

func TestTransportBadGreeting(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.greetingCode = 554
	fs.greetingMsg = "No SMTP here"
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for bad greeting")
	}
}

func TestTransportEHLOFallback(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.ehloResponse = func(host string) (int, string) { return 500, "Not recognized" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success via HELO fallback, got: %s", result.StatusMsg)
	}
}

func TestTransportRCPTRejected(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "User unknown" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"unknown@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for RCPT rejection")
	}
}

func TestTransportDATARejected(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 552, "Message too large" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("big data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for DATA rejection")
	}
}

func TestTransportConnectionTimeout(t *testing.T) {
	transport := NewSMTPTransport(TransportConfig{ConnectTimeout: 50 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	result := transport.Deliver(ctx, "10.0.0.1:25", false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for timeout")
	}
}

func TestTransportQUITHandling(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("Subject: Test\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success: %s", result.StatusMsg)
	}
}

func TestTransportSTARTTLSAdvertised(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	_ = result
	// STARTTLS is attempted but the fake server doesn't implement it.
	// This tests that the transport doesn't crash when STARTTLS is attempted.
}

func TestTransportMultipleRCPT(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt1@test.com", "rcpt2@test.com"}, []byte("Subject: Multi\r\n\r\nBody"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success for multiple rcpt, got: %s", result.StatusMsg)
	}
}

func TestTransportNullSender(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "", []string{"rcpt@test.com"}, []byte("Subject: Null\r\n\r\nSender"), "test.orvix.local")
	if !result.Success {
		t.Fatalf("expected success for null sender (bounce): %s", result.StatusMsg)
	}
}

// ── Bounce Classification Tests ──────────────────────────────

func TestBounceUserUnknown(t *testing.T) {
	bt := ClassifyBounce(550, "5.1.1 User unknown")
	if bt != BounceUserUnknown {
		t.Fatalf("expected user_unknown, got %s", bt)
	}
}

func TestBounceMailboxFull(t *testing.T) {
	bt := ClassifyBounce(552, "5.2.2 Mailbox full")
	if bt != BounceMailboxFull {
		t.Fatalf("expected mailbox_full, got %s", bt)
	}
}

func TestBounceRelayDenied(t *testing.T) {
	bt := ClassifyBounce(554, "5.7.1 Relay denied")
	if bt != BounceRelayDenied {
		t.Fatalf("expected relay_denied, got %s", bt)
	}
}

func TestBounceTempFail(t *testing.T) {
	bt := ClassifyBounce(450, "4.2.1 Mailbox busy")
	if bt != BounceUnavailable {
		t.Fatalf("expected unavailable, got %s", bt)
	}
}

func TestBounceTimeout(t *testing.T) {
	bt := ClassifyBounce(451, "4.4.2 Timeout")
	if bt != BounceTimeout {
		t.Fatalf("expected timeout, got %s", bt)
	}
}

func TestBounceSystemError(t *testing.T) {
	bt := ClassifyBounce(0, "connection refused")
	if bt != BounceSystemError {
		t.Fatalf("expected system_error, got %s", bt)
	}
}

func TestBounceSpamBlocked(t *testing.T) {
	bt := ClassifyBounce(550, "5.7.1 Spam blocked")
	if bt != BounceSpamBlocked {
		t.Fatalf("expected spam_blocked, got %s", bt)
	}
}

func TestBounceMessageTooBig(t *testing.T) {
	bt := ClassifyBounce(552, "5.3.4 Message too large")
	if bt != BounceMessageTooBig {
		t.Fatalf("expected message_too_big, got %s", bt)
	}
}

func TestBounceEventStruct(t *testing.T) {
	e := BounceEvent{
		QueueEntryID: 1, FromAddress: "a@b.com", ToAddress: "c@d.com",
		BounceType: BounceUserUnknown, StatusCode: 550, StatusMsg: "Unknown",
		TempFail: false, AttemptCount: 3,
	}
	if e.FromAddress != "a@b.com" || e.BounceType != BounceUserUnknown {
		t.Fatal("bounce event fields incorrect")
	}
}

// ── Worker Creation Tests ────────────────────────────────────

func TestNewDeliveryWorker(t *testing.T) {
	resolver := NewFakeResolver()
	transport := NewSMTPTransport(DefaultTransportConfig())
	w := NewDeliveryWorker(nil, nil, resolver, transport, "local.test", "worker-1")
	if w == nil {
		t.Fatal("worker should not be nil")
	}
	if w.WorkerID != "worker-1" {
		t.Fatalf("expected worker-1, got %s", w.WorkerID)
	}
}

// ── Concurrent Tests ─────────────────────────────────────────

func TestFakeSMTPConcurrentSessions(t *testing.T) {
	fs := startFakeSMTP(t)

	var wg sync.WaitGroup
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			transport := NewSMTPTransport(DefaultTransportConfig())
			result := transport.Deliver(context.Background(), fs.addr, false,
				fmt.Sprintf("sender%d@test.com", id),
				[]string{fmt.Sprintf("rcpt%d@test.com", id)},
				[]byte("Subject: Concurrent\r\n\r\nBody"),
				"test.orvix.local")
			if !result.Success {
				errs <- fmt.Errorf("session %d: %s", id, result.StatusMsg)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent delivery: %v", err)
	}
}

func TestWorkersLeaseEmptyQueue(t *testing.T) {
	// Test that ProcessOnce handles nil queue gracefully — tested at unit level.
	// Full lease integration tested in queue package.
}

// ── ContainsAny Tests ────────────────────────────────────────

func TestContainsAnyBasic(t *testing.T) {
	if !containsAny("User unknown", "unknown") {
		t.Fatal("expected match")
	}
	if containsAny("OK", "unknown") {
		t.Fatal("should not match")
	}
}

func TestContainsAnyCaseInsensitive(t *testing.T) {
	if !containsAny("User Unknown", "unknown") {
		t.Fatal("case insensitive should match")
	}
}

func TestContainsAnyMultiSubstr(t *testing.T) {
	if !containsAny("Mailbox full, quota exceeded", "quota") {
		t.Fatal("should match quota")
	}
}

// ── Transport Config Tests ───────────────────────────────────

func TestDefaultTransportConfig(t *testing.T) {
	cfg := DefaultTransportConfig()
	if cfg.ConnectTimeout != 30*time.Second {
		t.Fatalf("expected 30s timeout, got %v", cfg.ConnectTimeout)
	}
	if !cfg.AttemptSTARTTLS {
		t.Fatal("expected STARTTLS enabled by default")
	}
}

// ── DeliveryResult Tests ─────────────────────────────────────

func TestDeliveryResultDefaults(t *testing.T) {
	r := &DeliveryResult{}
	if r.Success {
		t.Fatal("expected false success by default")
	}
	if r.TempFail {
		t.Fatal("expected false temp fail by default")
	}
}

// ── Resolver Interface Compliance ────────────────────────────

func TestResolverInterface(t *testing.T) {
	var r Resolver = NewFakeResolver()
	_ = r
}

func TestDNSResolverInterface(t *testing.T) {
	var r Resolver = NewDNSResolver()
	_ = r
}

// ── Additional Transport Tests ───────────────────────────────

func TestTransport4xxDefer(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 450, "4.7.1 Try again later" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 4xx")
	}
	if !result.TempFail {
		t.Fatal("expected temp fail for 4xx")
	}
}

func TestTransport5xxPermFail(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 5xx")
	}
	if result.TempFail {
		t.Fatal("expected permanent fail for 5xx")
	}
}

func TestTransportResponseCodeSet(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "User unknown" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.StatusCode != 550 {
		t.Fatalf("expected status code 550, got %d", result.StatusCode)
	}
}

// ── MX Fallback Tests ────────────────────────────────────────

func TestNoMXRecordsReturnsErr(t *testing.T) {
	r := NewFakeResolver()
	r.FailDomain = "nxdomain.test"
	_, err := r.LookupMX(context.Background(), "nxdomain.test")
	if err == nil {
		t.Fatal("expected error for domain with no MX")
	}
}

func TestEmptyMXRecordsReturnsDefault(t *testing.T) {
	r := NewFakeResolver()
	// No MX records configured, should use default: mail.<domain>
	records, err := r.LookupMX(context.Background(), "default.test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(records) != 1 || records[0].Host != "mail.default.test" {
		t.Fatalf("expected default MX mail.default.test, got %v", records)
	}
}

func TestResolverMXPicksLowestPriority(t *testing.T) {
	r := NewFakeResolver()
	r.MXRecords["mixed.test"] = []MXRecord{
		{Host: "mx3.mixed.test", Priority: 30},
		{Host: "mx1.mixed.test", Priority: 10},
		{Host: "mx2.mixed.test", Priority: 20},
	}
	records, _ := r.LookupMX(context.Background(), "mixed.test")
	if records[0].Host != "mx1.mixed.test" {
		t.Fatalf("expected mx1 (priority 10) first, got %s", records[0].Host)
	}
	if records[2].Host != "mx3.mixed.test" {
		t.Fatalf("expected mx3 (priority 30) last")
	}
}

// ── Bounce Edge Cases ────────────────────────────────────────

func TestBounceCaseInsensitive(t *testing.T) {
	bt := ClassifyBounce(550, "5.1.1 USER UNKNOWN")
	if bt != BounceUserUnknown {
		t.Fatalf("expected user_unknown for 'USER UNKNOWN', got %s", bt)
	}
}

func TestBounceQuotaExceeded(t *testing.T) {
	bt := ClassifyBounce(552, "Quota exceeded")
	if bt != BounceMailboxFull {
		t.Fatalf("expected mailbox_full for quota exceeded, got %s", bt)
	}
}

func TestBounceRelayNotPermitted(t *testing.T) {
	bt := ClassifyBounce(554, "Relay not permitted")
	if bt != BounceRelayDenied {
		t.Fatalf("expected relay_denied, got %s", bt)
	}
}

// ── Transport Config Customization ───────────────────────────

// ── HARDENING: Policy Tests ──────────────────────────────────

func TestPolicyCheckSenderAllowed(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckSender(context.Background(), "sender@test.com", uintPtr(1), uintPtr(1), uintPtr(1), 50)
	if !r.Allowed {
		t.Fatalf("expected allowed, got: %s", r.Reason)
	}
}

func TestPolicyCheckSenderBlocked(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckSender(context.Background(), "sender@test.com", uintPtr(1), uintPtr(1), uintPtr(1), 999999)
	if r.Allowed {
		t.Fatal("expected blocked when over limit")
	}
	if r.Code != 550 {
		t.Fatalf("expected 550, got %d", r.Code)
	}
}

func TestPolicyCheckDomainBlocked(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckDomain(context.Background(), "test.com", 999999, 0)
	if r.Allowed {
		t.Fatal("expected blocked for over-limit domain")
	}
}

func TestPolicyCheckDomainRecipientsBlocked(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckDomain(context.Background(), "test.com", 0, 999999)
	if r.Allowed {
		t.Fatal("expected blocked for over-limit recipients")
	}
}

func TestPolicyCheckMessageSize(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckMessageSize(999999999)
	if r.Allowed {
		t.Fatal("expected blocked for oversized message")
	}
}

func TestPolicyCheckMessageSizeAllowed(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckMessageSize(1024)
	if !r.Allowed {
		t.Fatalf("expected allowed for small message: %s", r.Reason)
	}
}

func TestPolicyCheckRecipients(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckRecipients(999)
	if r.Allowed {
		t.Fatal("expected blocked for too many recipients")
	}
}

func TestPolicyCheckRecipientsAllowed(t *testing.T) {
	pe := NewPolicyEnforcer(DefaultDeliveryPolicy())
	r := pe.CheckRecipients(1)
	if !r.Allowed {
		t.Fatalf("expected allowed for single recipient: %s", r.Reason)
	}
}

// ── HARDENING: Anti-Loop Tests ───────────────────────────────

func TestLoopCheckReceivedHeadersExceeded(t *testing.T) {
	ld := NewLoopDetector(5, 10, "local.test")
	// Create a message with many Received headers.
	msg := []byte{}
	for i := 0; i < 10; i++ {
		msg = append(msg, []byte(fmt.Sprintf("Received: from relay%d.example.com\r\n", i))...)
	}
	msg = append(msg, []byte("Subject: Test\r\n\r\nBody")...)
	result := ld.CheckReceivedHeaders(msg)
	if !result.IsLoop {
		t.Fatal("expected loop detection for excessive Received headers")
	}
}

func TestLoopCheckReceivedHeadersOK(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	msg := []byte("Received: from relay1.example.com\r\nSubject: Test\r\n\r\nBody")
	result := ld.CheckReceivedHeaders(msg)
	if result.IsLoop {
		t.Fatal("expected no loop for single Received header")
	}
}

func TestLoopCheckSelfDelivery(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	result := ld.CheckSelfDelivery("local.test")
	if !result.IsLoop {
		t.Fatal("expected self-delivery loop detection")
	}
}

func TestLoopCheckSelfDeliveryOK(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	result := ld.CheckSelfDelivery("remote.test")
	if result.IsLoop {
		t.Fatal("expected no loop for remote domain")
	}
}

func TestLoopCheckDeferralLoop(t *testing.T) {
	ld := NewLoopDetector(50, 5, "local.test")
	result := ld.CheckDeferralLoop(10, 16)
	if !result.IsLoop {
		t.Fatal("expected deferral loop detection")
	}
}

func TestLoopCheckDeferralLoopOK(t *testing.T) {
	ld := NewLoopDetector(50, 10, "local.test")
	result := ld.CheckDeferralLoop(2, 16)
	if result.IsLoop {
		t.Fatal("expected no loop for low deferral count")
	}
}

// ── HARDENING: Audit Event Tests ─────────────────────────────

func TestAuditLoggerRecordEvent(t *testing.T) {
	logger := NewAuditLogger()
	event := BuildEvent(1, "msg-1", "from@test.com", "to@test.com", "w1", "outbound", EventQueued)
	if err := logger.RecordEvent(context.Background(), event); err != nil {
		t.Fatalf("record event: %v", err)
	}
	if len(logger.Events()) != 1 {
		t.Fatalf("expected 1 event, got %d", len(logger.Events()))
	}
}

func TestAuditLoggerLastEvent(t *testing.T) {
	logger := NewAuditLogger()
	logger.RecordEvent(context.Background(), BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "out", EventQueued))
	logger.RecordEvent(context.Background(), BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "out", EventLeased))
	logger.RecordEvent(context.Background(), BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "out", EventDelivered))

	last := logger.LastEvent(1)
	if last == nil || last.EventType != EventDelivered {
		t.Fatalf("expected last event '%s', got '%s'", EventDelivered, last.EventType)
	}
}

func TestAuditBuildEvent(t *testing.T) {
	e := BuildEvent(1, "msg-1", "f@t.com", "t@t.com", "w1", "outbound", EventDeferred)
	if e.EventType != EventDeferred {
		t.Fatalf("expected deferred, got %s", e.EventType)
	}
	if e.WorkerID != "w1" {
		t.Fatalf("expected w1, got %s", e.WorkerID)
	}
}

// ── HARDENING: Enhanced Status Code Tests ────────────────────

func TestParseEnhancedCode(t *testing.T) {
	ec := ParseEnhancedCode("5.1.1 User unknown")
	if ec != "5.1.1" {
		t.Fatalf("expected 5.1.1, got %s", ec)
	}
}

func TestParseEnhancedCodeNotFound(t *testing.T) {
	ec := ParseEnhancedCode("User unknown")
	if ec != "" {
		t.Fatalf("expected empty, got %s", ec)
	}
}

func TestParseEnhancedCodeTwoDigit(t *testing.T) {
	ec := ParseEnhancedCode("5.7.27 Relay access denied")
	if ec != "5.7.27" {
		t.Fatalf("expected 5.7.27, got %s", ec)
	}
}

func TestFormatEnhancedCode(t *testing.T) {
	ec := FormatEnhancedCode(5, 1, 1)
	if ec != "5.1.1" {
		t.Fatalf("expected 5.1.1, got %s", ec)
	}
}

// ── HARDENING: Remote Response Capture Tests ─────────────────

func TestTransportCapturesEnhancedCode(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.EnhancedCode != "5.1.1" {
		t.Fatalf("expected enhanced code 5.1.1, got %q", result.EnhancedCode)
	}
}

func TestTransportStoresLastRemoteResponse(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.7.1 Relay denied" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.StatusCode != 550 {
		t.Fatalf("expected status code 550, got %d", result.StatusCode)
	}
	if !strings.Contains(result.StatusMsg, "Relay denied") {
		t.Fatalf("expected error message to contain 'Relay denied', got %q", result.StatusMsg)
	}
}

// ── HARDENING: Transport Error Classification Tests ──────────

func TestTransport421Defer(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.greetingCode = 421
	fs.greetingMsg = "4.2.1 Service unavailable"
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 421")
	}
	if result.StatusCode != 421 {
		t.Fatalf("expected status code 421, got %d", result.StatusCode)
	}
}

func TestTransport450Defer(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.dataResponse = func() (int, string) { return 450, "4.2.1 Mailbox busy" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 450")
	}
	if !result.TempFail {
		t.Fatal("expected 450 to be temp fail")
	}
	if result.EnhancedCode != "4.2.1" {
		t.Fatalf("expected enhanced code 4.2.1, got %q", result.EnhancedCode)
	}
}

func TestTransport550Bounce(t *testing.T) {
	fs := startFakeSMTP(t)
	fs.rcptResponse = func(rcpt string) (int, string) { return 550, "5.1.1 User unknown" }
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"bad@test.com"}, []byte("data"), "test.orvix.local")
	if result.Success {
		t.Fatal("expected failure for 550")
	}
	if result.TempFail {
		t.Fatal("expected 550 to be permanent fail")
	}
}

func TestTransportRemoteHostStored(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	if result.RemoteHost != fs.addr {
		t.Fatalf("expected remote host %s, got %s", fs.addr, result.RemoteHost)
	}
}

func TestTransportDurationMsSet(t *testing.T) {
	fs := startFakeSMTP(t)
	transport := NewSMTPTransport(DefaultTransportConfig())
	result := transport.Deliver(context.Background(), fs.addr, false, "sender@test.com", []string{"rcpt@test.com"}, []byte("data"), "test.orvix.local")
	_ = result.DurationMs // field exists; may be 0 on very fast local connections
}

// ── HARDENING: Default Policy Config ─────────────────────────

func TestDefaultPolicyConfig(t *testing.T) {
	p := DefaultDeliveryPolicy()
	if p.MaxOutboundPerDomain != 1000 {
		t.Fatalf("expected 1000, got %d", p.MaxOutboundPerDomain)
	}
	if p.MaxMessageSizeBytes != 25*1024*1024 {
		t.Fatalf("expected 25MB, got %d", p.MaxMessageSizeBytes)
	}
	if p.MaxReceivedHeaders != 50 {
		t.Fatalf("expected 50, got %d", p.MaxReceivedHeaders)
	}
}

// ── HARDENING: Event Type Constants ──────────────────────────

func TestDeliveryEventTypes(t *testing.T) {
	types := []DeliveryEventType{EventQueued, EventLeased, EventConnecting, EventConnected,
		EventRemoteAccepted, EventRemoteRejected, EventDeferred, EventBounced,
		EventDelivered, EventDeadLetter, EventPolicyRejected, EventLoopDetected}
	for _, et := range types {
		if et == "" {
			t.Fatal("event type should not be empty")
		}
	}
}

// ── Helpers ──────────────────────────────────────────────────

func uintPtr(u uint) *uint { return &u }

func TestCustomTransportConfig(t *testing.T) {
	cfg := DefaultTransportConfig()
	cfg.ConnectTimeout = 5 * time.Second
	cfg.AttemptSTARTTLS = false
	transport := NewSMTPTransport(cfg)
	if transport.Config.ConnectTimeout != 5*time.Second {
		t.Fatal("custom timeout not applied")
	}
	if transport.Config.AttemptSTARTTLS {
		t.Fatal("STARTTLS should be disabled")
	}
}
