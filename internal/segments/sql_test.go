package segments

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)

// cartAbandoners is the example from the README: added to cart in the last 7 days,
// did not buy in the last 7 days, lives in Istanbul.
func cartAbandoners() Rules {
	return Rules{Match: MatchAll, Conditions: []Condition{
		{Type: TypeEvent, Event: "add_to_cart", WithinDays: intPtr(7), MinCount: intPtr(1)},
		{Type: TypeEvent, Event: "purchase", WithinDays: intPtr(7), MaxCount: intPtr(0)},
		{Type: TypeAttribute, Attribute: "city", Op: OpEquals, Value: "Istanbul"},
	}}
}

// TestMembersQuerySQL pins the exact SQL. If the builder changes, this test shows the new SQL
// in the diff, so a reviewer sees what the database will run.
func TestMembersQuerySQL(t *testing.T) {
	q := cartAbandoners().MembersQuery(now, "u_100", 50)

	wantSQL := `WITH ec AS (
    SELECT user_id,
           count(*) FILTER (WHERE name = $1 AND occurred_at >= $2) AS c0,
           count(*) FILTER (WHERE name = $4 AND occurred_at >= $5) AS c1
    FROM events
    WHERE (name = $1 AND occurred_at >= $2) OR (name = $4 AND occurred_at >= $5)
    GROUP BY user_id
)
SELECT u.id, u.last_seen_at
FROM users u LEFT JOIN ec ON ec.user_id = u.id
WHERE ((COALESCE(ec.c0, 0) >= $3) AND (COALESCE(ec.c1, 0) <= $6) AND (u.attributes @> $7::jsonb))
  AND u.id > $8
ORDER BY u.id
LIMIT $9`
	if q.SQL != wantSQL {
		t.Errorf("SQL =\n%s\n\nwant\n%s", q.SQL, wantSQL)
	}

	weekAgo := now.Add(-7 * 24 * time.Hour)
	// Parameters are numbered in the order conditions are compiled: each condition adds its own values.
	wantArgs := []any{"add_to_cart", weekAgo, 1, "purchase", weekAgo, 0, map[string]any{"city": "Istanbul"}, "u_100", 50}
	if !reflect.DeepEqual(q.Args, wantArgs) {
		t.Errorf("Args = %#v\nwant %#v", q.Args, wantArgs)
	}
}

func TestCountQueryAttributesOnlyAndAny(t *testing.T) {
	r := Rules{Match: MatchAny, Conditions: []Condition{
		{Type: TypeAttribute, Attribute: "plan", Op: OpNotEquals, Value: "free"},
		{Type: TypeAttribute, Attribute: "email", Op: OpExists},
	}}

	q := r.CountQuery(now)

	// No event conditions: no CTE and no join with events.
	wantSQL := "SELECT count(*)\nFROM users u\nWHERE (NOT u.attributes @> $1::jsonb) OR (u.attributes ? $2)"
	if q.SQL != wantSQL {
		t.Errorf("SQL =\n%s\nwant\n%s", q.SQL, wantSQL)
	}
}

func TestEventConditionWithoutWindowOrCounts(t *testing.T) {
	r := Rules{Match: MatchAll, Conditions: []Condition{{Type: TypeEvent, Event: "purchase"}}}

	q := r.CountQuery(now)

	// All time (no occurred_at filter) and "at least once" (>= 1).
	for _, want := range []string{"count(*) FILTER (WHERE name = $1) AS c0", "COALESCE(ec.c0, 0) >= $2"} {
		if !strings.Contains(q.SQL, want) {
			t.Errorf("SQL does not contain %q:\n%s", want, q.SQL)
		}
	}
	if !reflect.DeepEqual(q.Args, []any{"purchase", 1}) {
		t.Errorf("Args = %#v, want [purchase 1]", q.Args)
	}
}

// TestValuesNeverReachTheSQLText is the SQL injection check: hostile input ends up in Args only.
func TestValuesNeverReachTheSQLText(t *testing.T) {
	hostile := "x') OR 1=1; DROP TABLE users; --"
	r := Rules{Match: MatchAll, Conditions: []Condition{
		{Type: TypeAttribute, Attribute: hostile, Op: OpEquals, Value: hostile},
		{Type: TypeAttribute, Attribute: hostile, Op: OpExists},
	}}

	for _, q := range []Query{r.CountQuery(now), r.MembersQuery(now, hostile, 10)} {
		if strings.Contains(q.SQL, "DROP") || strings.Contains(q.SQL, "1=1") {
			t.Errorf("hostile input reached the SQL text:\n%s", q.SQL)
		}
	}
}
