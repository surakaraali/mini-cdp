// Package segments defines audience rules ("added to cart in the last 7 days but did not buy"),
// checks them, and turns them into parameterized SQL.
package segments

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/surakaraali/mini-cdp/internal/validate"
)

const (
	MatchAll = "all" // every condition must hold (AND)
	MatchAny = "any" // at least one condition must hold (OR)

	TypeEvent     = "event"
	TypeAttribute = "attribute"

	OpEquals    = "eq"
	OpNotEquals = "neq"
	OpExists    = "exists"

	maxConditions   = 10
	maxWithinDays   = 365
	maxNameLength   = 100
	maxDescLength   = 500
	maxAttributeLen = 64
)

// eventNamePattern is the same rule as for incoming events.
var eventNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// Rules is the audience definition stored with a segment, for example:
//
//	{"match": "all", "conditions": [
//	  {"type": "event", "event": "add_to_cart", "within_days": 7, "min_count": 1},
//	  {"type": "event", "event": "purchase", "within_days": 7, "max_count": 0},
//	  {"type": "attribute", "attribute": "city", "op": "eq", "value": "Istanbul"}
//	]}
type Rules struct {
	Match      string      `json:"match"`
	Conditions []Condition `json:"conditions"`
}

// Condition is one test a user must pass. Which fields apply depends on Type.
//
// The counts and the time window are pointers (*int) because 0 is a meaningful value:
// "max_count": 0 means "never did it", which is different from "max_count" not being set.
type Condition struct {
	Type string `json:"type"` // "event" or "attribute"

	// Event conditions: how many times the user did Event, optionally only in the last WithinDays days.
	// Without MinCount and MaxCount the condition means "did it at least once".
	Event      string `json:"event,omitempty"`
	WithinDays *int   `json:"within_days,omitempty"`
	MinCount   *int   `json:"min_count,omitempty"`
	MaxCount   *int   `json:"max_count,omitempty"`

	// Attribute conditions: a key in the user's attributes, e.g. city eq "Istanbul".
	Attribute string `json:"attribute,omitempty"`
	Op        string `json:"op,omitempty"`
	Value     any    `json:"value,omitempty"`
}

// bounds returns the effective count limits of an event condition. max is nil when there is no upper limit.
func (c Condition) bounds() (min int, max *int) {
	if c.MinCount == nil && c.MaxCount == nil {
		return 1, nil
	}
	if c.MinCount != nil {
		min = *c.MinCount
	}
	return min, c.MaxCount
}

// Validate checks the rules and returns every problem. An empty Match becomes "all".
func (r *Rules) Validate() validate.Errors {
	var errs validate.Errors

	switch r.Match {
	case "":
		r.Match = MatchAll
	case MatchAll, MatchAny:
	default:
		errs.Add("match", `must be "all" or "any"`)
	}

	switch n := len(r.Conditions); {
	case n == 0:
		errs.Add("conditions", "must contain at least 1 condition")
	case n > maxConditions:
		errs.Add("conditions", fmt.Sprintf("must contain at most %d conditions", maxConditions))
	}

	for i, c := range r.Conditions {
		errs = append(errs, c.validate().WithPrefix(fmt.Sprintf("conditions[%d].", i))...)
	}
	return errs
}

func (c Condition) validate() validate.Errors {
	var errs validate.Errors

	switch c.Type {
	case TypeEvent:
		if c.Event == "" {
			errs.Add("event", "is required")
		} else if !eventNamePattern.MatchString(c.Event) {
			errs.Add("event", "must be a snake_case event name")
		}
		if c.WithinDays != nil && (*c.WithinDays < 1 || *c.WithinDays > maxWithinDays) {
			errs.Add("within_days", fmt.Sprintf("must be between 1 and %d", maxWithinDays))
		}
		if c.MinCount != nil && *c.MinCount < 0 {
			errs.Add("min_count", "must not be negative")
		}
		if c.MaxCount != nil && *c.MaxCount < 0 {
			errs.Add("max_count", "must not be negative")
		}
		if c.MinCount != nil && c.MaxCount != nil && *c.MinCount > *c.MaxCount {
			errs.Add("min_count", "must not be greater than max_count")
		}
		if c.MinCount != nil && *c.MinCount == 0 && c.MaxCount == nil {
			errs.Add("min_count", "0 without max_count matches every user; remove the condition or set max_count")
		}
		if c.Attribute != "" || c.Op != "" || c.Value != nil {
			errs.Add("type", "event conditions cannot have attribute, op or value")
		}

	case TypeAttribute:
		switch {
		case c.Attribute == "":
			errs.Add("attribute", "is required")
		case utf8.RuneCountInString(c.Attribute) > maxAttributeLen:
			errs.Add("attribute", fmt.Sprintf("must be at most %d characters", maxAttributeLen))
		}
		switch c.Op {
		case OpEquals, OpNotEquals:
			if !isScalar(c.Value) {
				errs.Add("value", "must be a string, number or boolean")
			}
		case OpExists:
			if c.Value != nil {
				errs.Add("value", `must not be set when op is "exists"`)
			}
		default:
			errs.Add("op", `must be "eq", "neq" or "exists"`)
		}
		if c.Event != "" || c.WithinDays != nil || c.MinCount != nil || c.MaxCount != nil {
			errs.Add("type", "attribute conditions cannot have event, within_days, min_count or max_count")
		}

	default:
		errs.Add("type", `must be "event" or "attribute"`)
	}
	return errs
}

// isScalar reports whether v is a JSON string, number or boolean (not null, an object or an array).
// Numbers arrive as json.Number because the API decodes with UseNumber; float64 covers other callers.
func isScalar(v any) bool {
	switch v.(type) {
	case string, bool, json.Number, float64:
		return true
	default:
		return false
	}
}

// Segment is a saved, named set of rules.
type Segment struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Rules       Rules     `json:"rules"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Input is the body of a create or update request.
type Input struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Rules       Rules  `json:"rules"`
}

// Validate checks the input, including its rules.
func (in *Input) Validate() validate.Errors {
	var errs validate.Errors
	switch n := utf8.RuneCountInString(in.Name); {
	case n == 0:
		errs.Add("name", "is required")
	case n > maxNameLength:
		errs.Add("name", fmt.Sprintf("must be at most %d characters", maxNameLength))
	}
	if utf8.RuneCountInString(in.Description) > maxDescLength {
		errs.Add("description", fmt.Sprintf("must be at most %d characters", maxDescLength))
	}
	return append(errs, in.Rules.Validate().WithPrefix("rules.")...)
}

// Member is one user in a segment.
type Member struct {
	UserID     string    `json:"user_id"`
	LastSeenAt time.Time `json:"last_seen_at"`
}
