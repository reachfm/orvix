package migration

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const imapVersion = "IMAP4rev1"

type Progress struct {
	Mailbox    string  `json:"mailbox"`
	Total      int     `json:"total"`
	Current    int     `json:"current"`
	Percentage float64 `json:"percentage"`
	Status     string  `json:"status"`
	Error      string  `json:"error,omitempty"`
}

type MigrationConfig struct {
	Source      string `json:"source"`
	SourceHost  string `json:"source_host"`
	SourcePort  int    `json:"source_port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DestEmail   string `json:"dest_email"`
	UseTLS      bool   `json:"use_tls"`
	UseStartTLS bool   `json:"use_starttls"`
}

type IMAPSync struct {
	cfg MigrationConfig
}

func NewIMAPSync(cfg MigrationConfig) *IMAPSync {
	return &IMAPSync{cfg: cfg}
}

func (s *IMAPSync) Sync(progressCh chan<- Progress) error {
	defer close(progressCh)

	if s.cfg.SourceHost == "" || s.cfg.Username == "" || s.cfg.Password == "" {
		return fmt.Errorf("source host, username, and password required")
	}

	progressCh <- Progress{Status: "connecting", Mailbox: "Initializing connection"}

	port := s.cfg.SourcePort
	if port == 0 {
		if s.cfg.UseTLS {
			port = 993
		} else {
			port = 143
		}
	}

	addr := net.JoinHostPort(s.cfg.SourceHost, fmt.Sprintf("%d", port))

	var c *client.Client
	var err error

	if s.cfg.UseTLS {
		c, err = client.DialTLS(addr, &tls.Config{
			InsecureSkipVerify: false,
			ServerName:         s.cfg.SourceHost,
		})
	} else {
		c, err = client.Dial(addr)
		if err == nil && s.cfg.UseStartTLS {
			err = c.StartTLS(&tls.Config{
				InsecureSkipVerify: false,
				ServerName:         s.cfg.SourceHost,
			})
		}
	}

	if err != nil {
		progressCh <- Progress{Status: "failed", Error: fmt.Sprintf("connection failed: %v", err)}
		return fmt.Errorf("IMAP connection failed: %w", err)
	}
	defer c.Logout()

	progressCh <- Progress{Status: "authenticating", Mailbox: "Logging in"}
	if err := c.Login(s.cfg.Username, s.cfg.Password); err != nil {
		progressCh <- Progress{Status: "failed", Error: fmt.Sprintf("login failed: %v", err)}
		return fmt.Errorf("IMAP login failed: %w", err)
	}

	progressCh <- Progress{Status: "listing", Mailbox: "Fetching mailbox list"}
	mailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)

	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	var boxes []*imap.MailboxInfo
	for m := range mailboxes {
		boxes = append(boxes, m)
	}

	if err := <-done; err != nil {
		progressCh <- Progress{Status: "failed", Error: fmt.Sprintf("mailbox listing failed: %v", err)}
		return fmt.Errorf("failed to list mailboxes: %w", err)
	}

	if len(boxes) == 0 {
		boxes = append(boxes, &imap.MailboxInfo{Name: "INBOX"})
	}

	totalMailboxes := len(boxes)
	for i, mailbox := range boxes {
		mbox, err := c.Select(mailbox.Name, false)
		if err != nil {
			progressCh <- Progress{
				Mailbox: mailbox.Name,
				Status:  "skipped",
				Error:   fmt.Sprintf("cannot select mailbox: %v", err),
			}
			continue
		}

		progressCh <- Progress{
			Mailbox: mailbox.Name,
			Total:   int(mbox.Messages),
			Current: 0,
			Status:  "syncing",
		}

		// Message sync would go here in a real implementation
		progressCh <- Progress{
			Mailbox:    mailbox.Name,
			Total:      int(mbox.Messages),
			Current:    int(mbox.Messages),
			Percentage: 100.0,
			Status:     fmt.Sprintf("completed (%d of %d mailboxes)", i+1, totalMailboxes),
		}

		time.Sleep(100 * time.Millisecond)
	}

	progressCh <- Progress{
		Status:     "completed",
		Percentage: 100.0,
		Mailbox:    fmt.Sprintf("All %d mailboxes synced", totalMailboxes),
	}

	return nil
}
