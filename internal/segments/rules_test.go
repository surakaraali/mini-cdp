package segments

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func intPtr(n int) *int { return &n }

func TestRulesValidate(t *testing.T) {
	event := func(c Condition) Rules { c.Type = TypeEvent; return Rules{Conditions: []Condition{c}} }
	attr := func(c Condition) Rules { c.Type = TypeAttribute; return Rules{Conditions: []Condition{c}} }

	tests := []struct {
		name       string
		rules      Rules
		wantFields []string
	}{
		{"did the event at least once", event(Condition{Event: "purchase"}), nil},
		{"never did it in 7 days", event(Condition{Event: "purchase", WithinDays: intPtr(7), MaxCount: intPtr(0)}), nil},
		{"between 2 and 5 times", event(Condition{Event: "page_view", MinCount: intPtr(2), MaxCount: intPtr(5)}), nil},
		{"attribute equals a number", attr(Condition{Attribute: "age", Op: OpEquals, Value: json.Number("30")}), nil},
		{"attribute exists", attr(Condition{Attribute: "email", Op: OpExists}), nil},
		{"no conditions", Rules{}, []string{"conditions"}},
		{"unknown match", Rules{Match: "most", Conditions: event(Condition{Event: "purchase"}).Conditions}, []string{"match"}},
		{"too many conditions", Rules{Conditions: make([]Condition, 11)}, []string{"conditions", "conditions[0].type", "conditions[1].type", "conditions[2].type", "conditions[3].type", "conditions[4].type", "conditions[5].type", "conditions[6].type", "conditions[7].type", "conditions[8].type", "conditions[9].type", "conditions[10].type"}},
		{"unknown type", Rules{Conditions: []Condition{{Type: "page"}}}, []string{"conditions[0].type"}},
		{"event name missing", event(Condition{}), []string{"conditions[0].event"}},
		{"event name not snake_case", event(Condition{Event: "Add To Cart"}), []string{"conditions[0].event"}},
		{"within_days 0", event(Condition{Event: "purchase", WithinDays: intPtr(0)}), []string{"conditions[0].within_days"}},
		{"within_days over a year", event(Condition{Event: "purchase", WithinDays: intPtr(400)}), []string{"conditions[0].within_days"}},
		{"negative count", event(Condition{Event: "purchase", MinCount: intPtr(-1)}), []string{"conditions[0].min_count"}},
		{"min above max", event(Condition{Event: "purchase", MinCount: intPtr(3), MaxCount: intPtr(1)}), []string{"conditions[0].min_count"}},
		{"min 0 alone matches everyone", event(Condition{Event: "purchase", MinCount: intPtr(0)}), []string{"conditions[0].min_count"}},
		{"event condition with attribute fields", event(Condition{Event: "purchase", Op: OpEquals}), []string{"conditions[0].type"}},
		{"attribute missing", attr(Condition{Op: OpExists}), []string{"conditions[0].attribute"}},
		{"attribute too long", attr(Condition{Attribute: strings.Repeat("a", 65), Op: OpExists}), []string{"conditions[0].attribute"}},
		{"unknown op", attr(Condition{Attribute: "city", Op: "like", Value: "Ist%"}), []string{"conditions[0].op"}},
		{"eq without value", attr(Condition{Attribute: "city", Op: OpEquals}), []string{"conditions[0].value"}},
		{"eq with an object", attr(Condition{Attribute: "city", Op: OpEquals, Value: map[string]any{"a": 1}}), []string{"conditions[0].value"}},
		{"exists with a value", attr(Condition{Attribute: "city", Op: OpExists, Value: "x"}), []string{"conditions[0].value"}},
		{"attribute condition with event fields", attr(Condition{Attribute: "city", Op: OpExists, WithinDays: intPtr(7)}), []string{"conditions[0].type"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotFields []string
			for _, fe := range tt.rules.Validate() {
				gotFields = append(gotFields, fe.Field)
			}
			if !reflect.DeepEqual(gotFields, tt.wantFields) {
				t.Errorf("reported fields = %v, want %v", gotFields, tt.wantFields)
			}
		})
	}
}

func TestRulesValidateDefaultsMatchToAll(t *testing.T) {
	r := Rules{Conditions: []Condition{{Type: TypeEvent, Event: "purchase"}}}
	r.Validate()
	if r.Match != MatchAll {
		t.Errorf("Match = %q, want %q", r.Match, MatchAll)
	}
}

func TestInputValidate(t *testing.T) {
	in := Input{Name: "", Description: strings.Repeat("x", 501), Rules: Rules{}}
	var got []string
	for _, fe := range in.Validate() {
		got = append(got, fe.Field)
	}
	want := []string{"name", "description", "rules.conditions"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reported fields = %v, want %v", got, want)
	}
}
