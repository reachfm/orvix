package firewall

import (
	"encoding/json"
	"strings"
	"sync"
)

type Rule struct {
	Name      string                 `json:"name"`
	Condition string                 `json:"condition"`
	Field     string                 `json:"field"`
	Operator  string                 `json:"operator"`
	Value     string                 `json:"value"`
	Action    Action                 `json:"action"`
	Priority  int                    `json:"priority"`
	Enabled   bool                   `json:"enabled"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
}

type RulesEngine struct {
	mu    sync.RWMutex
	rules []Rule
}

func NewRulesEngine() *RulesEngine {
	return &RulesEngine{}
}

func (e *RulesEngine) AddRule(rule Rule) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.rules = append(e.rules, rule)
}

func (e *RulesEngine) RemoveRule(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var filtered []Rule
	for _, r := range e.rules {
		if r.Name != name {
			filtered = append(filtered, r)
		}
	}
	e.rules = filtered
}

func (e *RulesEngine) Evaluate(conn *Connection) (Action, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	matchValue := func(field string, conn *Connection) string {
		switch field {
		case "ip":
			return conn.IP
		case "country":
			return conn.Country
		case "ehlo":
			return conn.EHLO
		case "mail_from":
			return conn.MailFrom
		case "protocol":
			return conn.Protocol
		case "auth_user":
			return conn.AuthUser
		default:
			return ""
		}
	}

	for _, rule := range e.rules {
		if !rule.Enabled {
			continue
		}

		val := matchValue(rule.Field, conn)
		matched := false

		switch rule.Operator {
		case "equals":
			matched = val == rule.Value
		case "contains":
			matched = strings.Contains(val, rule.Value)
		case "prefix":
			matched = strings.HasPrefix(val, rule.Value)
		case "suffix":
			matched = strings.HasSuffix(val, rule.Value)
		case "not_equals":
			matched = val != rule.Value
		default:
			matched = false
		}

		if matched {
			return rule.Action, nil
		}
	}

	return ActionPass, nil
}

func (e *RulesEngine) ListRules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]Rule, len(e.rules))
	copy(result, e.rules)
	return result
}

func (e *RulesEngine) MarshalJSON() ([]byte, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return json.Marshal(e.rules)
}

func (e *RulesEngine) UnmarshalJSON(data []byte) error {
	var rules []Rule
	if err := json.Unmarshal(data, &rules); err != nil {
		return err
	}
	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()
	return nil
}

func RuleFromMap(m map[string]interface{}) (Rule, error) {
	rule := Rule{}
	if name, ok := m["name"].(string); ok {
		rule.Name = name
	}
	if field, ok := m["field"].(string); ok {
		rule.Field = field
	}
	if op, ok := m["operator"].(string); ok {
		rule.Operator = op
	}
	if val, ok := m["value"].(string); ok {
		rule.Value = val
	}
	if action, ok := m["action"].(string); ok {
		rule.Action = Action(action)
	}
	if priority, ok := m["priority"].(float64); ok {
		rule.Priority = int(priority)
	}
	if enabled, ok := m["enabled"].(bool); ok {
		rule.Enabled = enabled
	}
	rule.Enabled = true
	return rule, nil
}
