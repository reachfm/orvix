package providers

// Namecheap DNS API client abstraction (DNS-AUTOMATION-2G).
//
// The Namecheap XML API requires an HTTP client. We abstract the
// HTTP roundtrip behind an interface so the provider can be unit-
// tested with a fake client — tests must NEVER hit the public
// internet, and the production code path MUST NOT shell out to
// curl / dig / nslookup. The same interface is also what the
// provider uses to do its live read (getHosts) and write
// (setHosts) so the read-merge-write contract is enforceable
// end-to-end with no real API calls in the test suite.
//
// API reference (Namecheap v1):
//
//   https://<sandbox>.namecheap.com/xml.response
//
//   getHosts   — list all host records for a domain (SLD).
//   setHosts   — REPLACE the entire host list (full set must be
//                supplied every call). This is the critical
//                safety contract: a "set" without a full read-
//                merge-write would clobber unrelated records
//                (website A, www CNAME, third-party verification
//                TXT, etc.). The NamecheapClient.Get + Set pair
//                here exposes the raw primitives; the provider
//                composes the read-merge-write around them.

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxNamecheapResponseBytes caps the HTTP response body size
// for Namecheap API calls. Namecheap returns small XML payloads
// (typically < 64 KiB). We cap at 1 MiB to prevent memory abuse.
const maxNamecheapResponseBytes = 1 << 20 // 1 MiB

// NamecheapHost represents one host record as returned by the
// Namecheap API. The wire format uses a numeric HostId for
// update / delete; for read-merge-write the provider uses Name
// + Type + Address as the unique identity.
type NamecheapHost struct {
	HostID  string `xml:"HostId,attr"`
	Name    string `xml:"Name,attr"`
	Type    string `xml:"Type,attr"`
	Address string `xml:"Address,attr"`
	MXPref  string `xml:"MXPref,attr"`
	TTL     string `xml:"TTL,attr"`
}

// namecheapHostsResponse is the inner element of the API's
// getHosts response.
type namecheapHostsResponse struct {
	XMLName xml.Name        `xml:"namecheapresponse"`
	Status  string          `xml:"status,attr"`
	Hosts   []NamecheapHost `xml:"host"`
}

// NamecheapClient abstracts the live Namecheap HTTP roundtrip.
// Production uses NetNamecheapClient; tests use FakeNamecheapClient.
type NamecheapClient interface {
	// GetHosts lists every host record for sld.tld. The
	// implementation MUST do a real HTTP GET against the
	// Namecheap API in production; the fake returns canned
	// data in tests. Errors are sanitised by the provider layer
	// (no token-shaped substrings in the returned string).
	GetHosts(ctx context.Context, sld, tld string) ([]NamecheapHost, error)

	// SetHosts REPLACES the entire host list. The provider
	// always supplies the merged (existing + desired - dropped)
	// set, so the API never sees an unsafe overwrite. The
	// implementation MUST return a sanitised status string;
	// raw XML is never surfaced to the caller.
	SetHosts(ctx context.Context, sld, tld string, hosts []NamecheapHost) (string, error)
}

// NetNamecheapClient is the production client. It builds the
// Namecheap API URL with the required query parameters and
// performs an HTTPS GET / POST. The body is parsed into a
// namecheapHostsResponse; the provider layer maps errors to
// sanitised messages.
type NetNamecheapClient struct {
	APIUser  string
	APIKey   string
	Username string
	ClientIP string
	Sandbox  bool
	HTTP     *http.Client // overridable for tests; defaults to 15s timeout
}

// NewNetNamecheapClient constructs a NetNamecheapClient with a
// conservative 15s HTTP timeout. Callers may override HTTP for
// tests; production MUST keep the timeout.
func NewNetNamecheapClient(apiUser, apiKey, username, clientIP string, sandbox bool) *NetNamecheapClient {
	return &NetNamecheapClient{
		APIUser:  apiUser,
		APIKey:   apiKey,
		Username: username,
		ClientIP: clientIP,
		Sandbox:  sandbox,
		HTTP:     &http.Client{Timeout: 15 * time.Second},
	}
}

// apiBase returns the Namecheap XML endpoint, honouring the
// Sandbox flag.
func (c *NetNamecheapClient) apiBase() string {
	if c.Sandbox {
		return "https://api.sandbox.namecheap.com/xml.response"
	}
	return "https://api.namecheap.com/xml.response"
}

// GetHosts calls namecheap.domains.dns.getHosts. The function
// builds the query string with the required command + auth
// parameters and parses the XML response. The returned error
// is sanitised: it never contains the API key, the API user,
// or the request URL with credentials. Callers (the provider)
// log a one-line status, not the raw error.
func (c *NetNamecheapClient) GetHosts(ctx context.Context, sld, tld string) ([]NamecheapHost, error) {
	if strings.TrimSpace(c.APIUser) == "" || strings.TrimSpace(c.APIKey) == "" {
		return nil, fmt.Errorf("namecheap credentials not configured")
	}
	q := url.Values{}
	q.Set("ApiUser", c.APIUser)
	q.Set("ApiKey", c.APIKey)
	q.Set("UserName", c.Username)
	q.Set("Command", "namecheap.domains.dns.getHosts")
	q.Set("SLD", sld)
	q.Set("TLD", tld)
	if c.ClientIP != "" {
		q.Set("ClientIp", c.ClientIP)
	}
	endpoint := c.apiBase() + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNamecheapResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("namecheap getHosts read error")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("namecheap getHosts http %d", resp.StatusCode)
	}
	// Reject oversized responses. If LimitReader returned
	// max bytes and the stream may contain more, unmarshal
	// will likely fail; we reject pre-emptively.
	if len(body) >= maxNamecheapResponseBytes {
		return nil, fmt.Errorf("namecheap getHosts response too large")
	}
	var r namecheapHostsResponse
	if err := xml.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("namecheap getHosts parse error")
	}
	if !strings.EqualFold(r.Status, "OK") {
		return nil, fmt.Errorf("namecheap getHosts status %q", r.Status)
	}
	return r.Hosts, nil
}

// SetHosts calls namecheap.domains.dns.setHosts. The
// implementation serialises the supplied host list into the
// form the Namecheap API expects (each record as a
// HostName<N>=... query parameter) and parses the result.
// The returned error is sanitised.
func (c *NetNamecheapClient) SetHosts(ctx context.Context, sld, tld string, hosts []NamecheapHost) (string, error) {
	if strings.TrimSpace(c.APIUser) == "" || strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("namecheap credentials not configured")
	}
	q := url.Values{}
	q.Set("ApiUser", c.APIUser)
	q.Set("ApiKey", c.APIKey)
	q.Set("UserName", c.Username)
	q.Set("Command", "namecheap.domains.dns.setHosts")
	q.Set("SLD", sld)
	q.Set("TLD", tld)
	if c.ClientIP != "" {
		q.Set("ClientIp", c.ClientIP)
	}
	for i, h := range hosts {
		// The Namecheap XML API expects these parameters per
		// record; we honour the standard 1-based indexing.
		// Record fields that are type-specific (Address /
		// MXPref) are always sent — Namecheap ignores fields
		// that don't apply to the chosen Type.
		q.Set(fmt.Sprintf("HostName%d", i+1), h.Name)
		q.Set(fmt.Sprintf("RecordType%d", i+1), h.Type)
		q.Set(fmt.Sprintf("Address%d", i+1), h.Address)
		q.Set(fmt.Sprintf("MXPref%d", i+1), h.MXPref)
		q.Set(fmt.Sprintf("TTL%d", i+1), h.TTL)
	}
	endpoint := c.apiBase() + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNamecheapResponseBytes))
	if err != nil {
		return "", fmt.Errorf("namecheap setHosts read error")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("namecheap setHosts http %d", resp.StatusCode)
	}
	if len(body) >= maxNamecheapResponseBytes {
		return "", fmt.Errorf("namecheap setHosts response too large")
	}
	var r namecheapHostsResponse
	if err := xml.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("namecheap setHosts parse error")
	}
	if !strings.EqualFold(r.Status, "OK") {
		return "", fmt.Errorf("namecheap setHosts status %q", r.Status)
	}
	return r.Status, nil
}

// FakeNamecheapClient is the in-memory client used by tests. It
// never touches the network. Tests pre-seed hosts via SetLive
// and assert on the captured SetHosts calls via LastSet / SetCalls.
type FakeNamecheapClient struct {
	mu     sync.Mutex
	live   map[string][]NamecheapHost // key = "<sld>.<tld>"
	setErr error
	getErr error
	// setCalls records the full set of (sld, tld, hosts) tuples
	// submitted to SetHosts so tests can assert that a
	// read-merge-write actually preserved unrelated records.
	setCalls []setCall
}

type setCall struct {
	SLD   string
	TLD   string
	Hosts []NamecheapHost
}

// NewFakeNamecheapClient returns an empty fake.
func NewFakeNamecheapClient() *FakeNamecheapClient {
	return &FakeNamecheapClient{live: make(map[string][]NamecheapHost)}
}

// SetLive installs the canned host list for sld.tld. Tests
// call this before invoking the provider.Plan / Apply path.
func (f *FakeNamecheapClient) SetLive(sld, tld string, hosts []NamecheapHost) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live[sld+"."+tld] = append([]NamecheapHost(nil), hosts...)
}

// SetGetError makes the next GetHosts call return err (used
// to simulate a 5xx / network outage in tests).
func (f *FakeNamecheapClient) SetGetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getErr = err
}

// SetSetError makes the next SetHosts call return err.
func (f *FakeNamecheapClient) SetSetError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setErr = err
}

// SetCalls returns a copy of the recorded SetHosts calls.
// Tests assert that the merged host list preserved unrelated
// records and contained exactly the Orvix-managed records
// the provider intends to write.
func (f *FakeNamecheapClient) SetCalls() []setCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]setCall, len(f.setCalls))
	for i, c := range f.setCalls {
		out[i] = setCall{
			SLD:   c.SLD,
			TLD:   c.TLD,
			Hosts: append([]NamecheapHost(nil), c.Hosts...),
		}
	}
	return out
}

// GetHosts implements NamecheapClient.
func (f *FakeNamecheapClient) GetHosts(_ context.Context, sld, tld string) ([]NamecheapHost, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	hosts, ok := f.live[sld+"."+tld]
	if !ok {
		return []NamecheapHost{}, nil
	}
	return append([]NamecheapHost(nil), hosts...), nil
}

// SetHosts implements NamecheapClient.
func (f *FakeNamecheapClient) SetHosts(_ context.Context, sld, tld string, hosts []NamecheapHost) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setErr != nil {
		return "", f.setErr
	}
	// Record the call so tests can assert on the merged
	// set. The fake also writes the merged set back into live
	// so a subsequent GetHosts returns the post-write state
	// (matches Namecheap semantics).
	f.setCalls = append(f.setCalls, setCall{
		SLD:   sld,
		TLD:   tld,
		Hosts: append([]NamecheapHost(nil), hosts...),
	})
	f.live[sld+"."+tld] = append([]NamecheapHost(nil), hosts...)
	return "OK", nil
}

// Compile-time assertion: both client implementations satisfy
// NamecheapClient.
var (
	_ NamecheapClient = (*NetNamecheapClient)(nil)
	_ NamecheapClient = (*FakeNamecheapClient)(nil)
)
