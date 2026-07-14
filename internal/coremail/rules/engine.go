package rules

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail/storage"
)

type ConditionType string

const (
	CondFromEquals      ConditionType = "from_equals"
	CondFromContains    ConditionType = "from_contains"
	CondToContains      ConditionType = "to_contains"
	CondSubjectContains ConditionType = "subject_contains"
	CondBodyContains    ConditionType = "body_contains"
	CondHasAttachment   ConditionType = "has_attachment"
)

type Condition struct {
	Type  ConditionType `json:"type"`
	Value string        `json:"value,omitempty"`
}

type ActionType string

const (
	ActionMoveToFolder ActionType = "move_to_folder"
	ActionCopyToFolder ActionType = "copy_to_folder"
	ActionSetFlag      ActionType = "set_flag"
	ActionForward      ActionType = "forward"
)

type Action struct {
	Type       ActionType            `json:"type"`
	FolderPath string                `json:"folder_path,omitempty"`
	SetFlag    *storage.SetFlagValue `json:"set_flag,omitempty"`
	ForwardTo  string                `json:"forward_to,omitempty"`
}

type ParsedRule struct {
	ID             uint
	MailboxID      uint
	Name           string
	Enabled        bool
	SortOrder      int
	StopProcessing bool
	Conditions     []Condition
	Actions        []Action
}

func ParseRuleJSON(ruleID uint, mailboxID uint, name string, enabled bool, sortOrder int, stopProcessing bool, conditionsJSON, actionsJSON string) (*ParsedRule, error) {
	var conds []Condition
	if err := json.Unmarshal([]byte(conditionsJSON), &conds); err != nil {
		return nil, fmt.Errorf("rule %d conditions json: %w", ruleID, err)
	}
	var acts []Action
	if err := json.Unmarshal([]byte(actionsJSON), &acts); err != nil {
		return nil, fmt.Errorf("rule %d actions json: %w", ruleID, err)
	}
	for i, c := range conds {
		switch c.Type {
		case CondFromEquals, CondFromContains, CondToContains,
			CondSubjectContains, CondBodyContains, CondHasAttachment:
		default:
			return nil, fmt.Errorf("rule %d condition %d: unknown type %q", ruleID, i, c.Type)
		}
		if c.Type != CondHasAttachment && strings.TrimSpace(c.Value) == "" {
			return nil, fmt.Errorf("rule %d condition %d (%s): value is required", ruleID, i, c.Type)
		}
	}
	for i, a := range acts {
		switch a.Type {
		case ActionMoveToFolder, ActionCopyToFolder:
			if strings.TrimSpace(a.FolderPath) == "" {
				return nil, fmt.Errorf("rule %d action %d (%s): folder_path is required", ruleID, i, a.Type)
			}
		case ActionSetFlag:
			if a.SetFlag == nil || (a.SetFlag.Seen == nil && a.SetFlag.Flagged == nil) {
				return nil, fmt.Errorf("rule %d action %d: set_flag needs at least one of seen/flagged", ruleID, i)
			}
		case ActionForward:
			if _, err := mail.ParseAddress(a.ForwardTo); err != nil {
				return nil, fmt.Errorf("rule %d action %d: forward_to invalid: %w", ruleID, i, err)
			}
		default:
			return nil, fmt.Errorf("rule %d action %d: unknown type %q", ruleID, i, a.Type)
		}
	}
	return &ParsedRule{
		ID:             ruleID,
		MailboxID:      mailboxID,
		Name:           name,
		Enabled:        enabled,
		SortOrder:      sortOrder,
		StopProcessing: stopProcessing,
		Conditions:     conds,
		Actions:        acts,
	}, nil
}

type MessageContext struct {
	From          string
	To            string
	Cc            string
	Subject       string
	Body          string
	HasAttachment bool
	ReceivedAt    time.Time
	MessageID     string
}

type EngineAction struct {
	SetFlag       *storage.SetFlagValue
	MoveTo        string
	CopyTo        string
	ForwardTo     string
	VacationReply *VacationReply
}

type VacationReply struct {
	Subject string
	Body    string
}

type Result struct {
	Actions          []EngineAction
	StopProcessing   bool
	ForwardedAlready bool
}

func Evaluate(rules []*ParsedRule, ctx MessageContext) *Result {
	r := &Result{}
	for _, rule := range rules {
		if rule == nil || !rule.Enabled {
			continue
		}
		if !matchAll(rule.Conditions, ctx) {
			continue
		}
		for _, a := range rule.Actions {
			out := EngineAction{}
			switch a.Type {
			case ActionMoveToFolder:
				out.MoveTo = a.FolderPath
			case ActionCopyToFolder:
				out.CopyTo = a.FolderPath
			case ActionSetFlag:
				v := storage.SetFlagValue{}
				if a.SetFlag != nil {
					if a.SetFlag.Seen != nil {
						s := *a.SetFlag.Seen
						v.Seen = &s
					}
					if a.SetFlag.Flagged != nil {
						f := *a.SetFlag.Flagged
						v.Flagged = &f
					}
				}
				out.SetFlag = &v
			case ActionForward:
				out.ForwardTo = a.ForwardTo
			}
			r.Actions = append(r.Actions, out)
			if a.Type == ActionForward {
				r.ForwardedAlready = true
			}
		}
		if rule.StopProcessing {
			r.StopProcessing = true
			break
		}
	}
	return r
}

func matchAll(conds []Condition, ctx MessageContext) bool {
	if len(conds) == 0 {
		return false
	}
	from := strings.ToLower(strings.TrimSpace(ctx.From))
	to := strings.ToLower(strings.TrimSpace(ctx.To))
	subject := strings.ToLower(ctx.Subject)
	body := strings.ToLower(ctx.Body)
	for _, c := range conds {
		switch c.Type {
		case CondFromEquals:
			if from != strings.ToLower(strings.TrimSpace(c.Value)) {
				return false
			}
		case CondFromContains:
			if !strings.Contains(from, strings.ToLower(c.Value)) {
				return false
			}
		case CondToContains:
			if !strings.Contains(to, strings.ToLower(c.Value)) {
				return false
			}
		case CondSubjectContains:
			if !strings.Contains(subject, strings.ToLower(c.Value)) {
				return false
			}
		case CondBodyContains:
			if body == "" {
				return false
			}
			if !strings.Contains(body, strings.ToLower(c.Value)) {
				return false
			}
		case CondHasAttachment:
			want := strings.ToLower(strings.TrimSpace(c.Value)) == "true"
			if ctx.HasAttachment != want {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func ValidateConditionsJSON(raw string) error {
	var conds []Condition
	if err := json.Unmarshal([]byte(raw), &conds); err != nil {
		return fmt.Errorf("conditions json: %w", err)
	}
	for i, c := range conds {
		switch c.Type {
		case CondFromEquals, CondFromContains, CondToContains,
			CondSubjectContains, CondBodyContains, CondHasAttachment:
		default:
			return fmt.Errorf("condition %d: unknown type %q", i, c.Type)
		}
		if c.Type != CondHasAttachment && strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("condition %d (%s): value is required", i, c.Type)
		}
	}
	return nil
}

func ValidateActionsJSON(raw string) error {
	var acts []Action
	if err := json.Unmarshal([]byte(raw), &acts); err != nil {
		return fmt.Errorf("actions json: %w", err)
	}
	if len(acts) == 0 {
		return fmt.Errorf("actions: at least one action is required")
	}
	for i, a := range acts {
		switch a.Type {
		case ActionMoveToFolder, ActionCopyToFolder:
			if strings.TrimSpace(a.FolderPath) == "" {
				return fmt.Errorf("action %d (%s): folder_path is required", i, a.Type)
			}
			if strings.ContainsAny(a.FolderPath, "/\\") ||
				a.FolderPath == "." || a.FolderPath == ".." {
				return fmt.Errorf("action %d: folder_path %q is not allowed", i, a.FolderPath)
			}
		case ActionSetFlag:
			if a.SetFlag == nil || (a.SetFlag.Seen == nil && a.SetFlag.Flagged == nil) {
				return fmt.Errorf("action %d: set_flag needs at least one of seen/flagged", i)
			}
		case ActionForward:
			if _, err := mail.ParseAddress(a.ForwardTo); err != nil {
				return fmt.Errorf("action %d: forward_to invalid: %w", i, err)
			}
		default:
			return fmt.Errorf("action %d: unknown type %q", i, a.Type)
		}
	}
	return nil
}
