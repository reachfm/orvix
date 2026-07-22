package firewall

import (
	"fmt"
	"sync"
	"time"
)

type Engine struct {
	layers      []Layer
	rulesEngine *RulesEngine
	reputation  *ReputationService
	geoBlock    *GeoBlockService
	mu          sync.RWMutex
}

type Layer interface {
	Name() string
	Filter(conn *Connection) (Action, error)
}

type Action string

const (
	ActionPass       Action = "pass"
	ActionQuarantine Action = "quarantine"
	ActionBlock      Action = "block"
	ActionThrottle   Action = "throttle"
)

type Connection struct {
	IP          string            `json:"ip"`
	Port        int               `json:"port"`
	Protocol    string            `json:"protocol"`
	EHLO        string            `json:"ehlo"`
	MailFrom    string            `json:"mail_from"`
	RcptTo      []string          `json:"rcpt_to"`
	Country     string            `json:"country"`
	TLSCipher   string            `json:"tls_cipher"`
	TLSCert     string            `json:"tls_cert"`
	AuthUser    string            `json:"auth_user"`
	AuthMethod  string            `json:"auth_method"`
	Headers     map[string]string `json:"headers"`
	BodyPreview string            `json:"body_preview"`
	Attachments []Attachment      `json:"attachments"`
	SPFResult   string            `json:"spf_result"`
	DKIMResult  string            `json:"dkim_result"`
	DMARCResult string            `json:"dmarc_result"`
	SpamScore   float64           `json:"spam_score"`
	MsgCount24h int               `json:"msg_count_24h"`
}

type Attachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	Extension   string `json:"extension"`
}

type LogEntry struct {
	Time       time.Time   `json:"time"`
	IP         string      `json:"ip"`
	Action     Action      `json:"action"`
	Layer      string      `json:"layer"`
	Reason     string      `json:"reason"`
	Connection *Connection `json:"connection"`
}

type layerImpl struct {
	name   string
	filter func(conn *Connection) (Action, error)
}

func (l *layerImpl) Name() string                            { return l.name }
func (l *layerImpl) Filter(conn *Connection) (Action, error) { return l.filter(conn) }

func NewEngine() *Engine {
	return &Engine{
		rulesEngine: NewRulesEngine(),
		reputation:  NewReputationService(),
		geoBlock:    NewGeoBlockService(),
	}
}

func (e *Engine) Init() {
	e.layers = []Layer{
		&layerImpl{name: "connection", filter: e.connectionFilter},
		&layerImpl{name: "protocol", filter: e.protocolFilter},
		&layerImpl{name: "auth", filter: e.authFilter},
		&layerImpl{name: "content", filter: e.contentFilter},
		&layerImpl{name: "behavioral", filter: e.behavioralFilter},
	}
}

func (e *Engine) Process(conn *Connection) (Action, *LogEntry, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if conn.Headers == nil {
		conn.Headers = make(map[string]string)
	}

	for _, layer := range e.layers {
		action, err := layer.Filter(conn)
		if err != nil {
			return ActionBlock, &LogEntry{
				Time:   time.Now(),
				IP:     conn.IP,
				Action: ActionBlock,
				Layer:  layer.Name(),
				Reason: fmt.Sprintf("error: %v", err),
			}, fmt.Errorf("layer %s error: %w", layer.Name(), err)
		}

		if action != ActionPass {
			return action, &LogEntry{
				Time:       time.Now(),
				IP:         conn.IP,
				Action:     action,
				Layer:      layer.Name(),
				Reason:     fmt.Sprintf("blocked by %s layer", layer.Name()),
				Connection: conn,
			}, nil
		}

		rulesAction, err := e.rulesEngine.Evaluate(conn)
		if err == nil && rulesAction != ActionPass {
			return rulesAction, &LogEntry{
				Time:       time.Now(),
				IP:         conn.IP,
				Action:     rulesAction,
				Layer:      "rules",
				Reason:     "matched custom rule",
				Connection: conn,
			}, nil
		}
	}

	return ActionPass, nil, nil
}

func (e *Engine) Layers() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	names := make([]string, len(e.layers))
	for i, l := range e.layers {
		names[i] = l.Name()
	}
	return names
}

func (e *Engine) RulesEngine() *RulesEngine      { return e.rulesEngine }
func (e *Engine) Reputation() *ReputationService { return e.reputation }
func (e *Engine) GeoBlock() *GeoBlockService     { return e.geoBlock }
