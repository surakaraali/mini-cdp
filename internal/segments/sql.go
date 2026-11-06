package segments

import (
	"fmt"
	"strings"
	"time"
)

// Query is SQL text plus its parameters, ready to pass to pgx.
type Query struct {
	SQL  string
	Args []any
}

// params collects query parameters and hands out their placeholders ($1, $2, ...).
// Every value from the rules goes through here, never into the SQL text,
// so a value like "x' OR '1'='1" is only ever compared as data. That is what prevents SQL injection.
// The SQL text itself is built only from fixed pieces in this file.
type params struct{ args []any }

func (p *params) add(v any) string {
	p.args = append(p.args, v)
	return fmt.Sprintf("$%d", len(p.args))
}

// compiled is the part shared by the members and count queries.
type compiled struct {
	with  string // "WITH ec AS (...)" or "" when there are no event conditions
	from  string
	where string
	p     *params
}

// compile turns valid rules into SQL. now fixes "the last N days" for every condition of one query.
//
// Event conditions are answered in one pass over the events table: the CTE "ec" (event counts)
// has one row per user and one count column per event condition. Users with no matching events
// are not in ec, so the LEFT JOIN gives them NULL, and COALESCE turns that into 0.
// That is how "max_count": 0 ("never bought") finds users who have no purchase rows at all.
func (r Rules) compile(now time.Time) compiled {
	p := &params{}
	var (
		countColumns []string // count(*) FILTER (WHERE ...) AS c0
		eventFilters []string // (name = $1 AND occurred_at >= $2)
		conditions   []string // one SQL boolean per condition
	)

	for i, c := range r.Conditions {
		switch c.Type {
		case TypeEvent:
			filter := "name = " + p.add(c.Event)
			if c.WithinDays != nil {
				since := now.Add(-time.Duration(*c.WithinDays) * 24 * time.Hour)
				filter += " AND occurred_at >= " + p.add(since)
			}
			column := fmt.Sprintf("c%d", i)
			countColumns = append(countColumns, fmt.Sprintf("count(*) FILTER (WHERE %s) AS %s", filter, column))
			eventFilters = append(eventFilters, "("+filter+")")

			count := fmt.Sprintf("COALESCE(ec.%s, 0)", column)
			min, max := c.bounds()
			var checks []string
			if min > 0 {
				checks = append(checks, count+" >= "+p.add(min))
			}
			if max != nil {
				checks = append(checks, count+" <= "+p.add(*max))
			}
			conditions = append(conditions, "("+strings.Join(checks, " AND ")+")")

		case TypeAttribute:
			switch c.Op {
			case OpEquals:
				// @> means "contains": {"city":"Istanbul","plan":"pro"} @> {"city":"Istanbul"} is true.
				conditions = append(conditions, "(u.attributes @> "+p.add(map[string]any{c.Attribute: c.Value})+"::jsonb)")
			case OpNotEquals:
				conditions = append(conditions, "(NOT u.attributes @> "+p.add(map[string]any{c.Attribute: c.Value})+"::jsonb)")
			case OpExists:
				conditions = append(conditions, "(u.attributes ? "+p.add(c.Attribute)+")")
			}
		}
	}

	joiner := " AND "
	if r.Match == MatchAny {
		joiner = " OR "
	}

	out := compiled{from: "users u", where: strings.Join(conditions, joiner), p: p}
	if len(countColumns) > 0 {
		out.with = "WITH ec AS (\n" +
			"    SELECT user_id,\n           " + strings.Join(countColumns, ",\n           ") + "\n" +
			"    FROM events\n" +
			"    WHERE " + strings.Join(eventFilters, " OR ") + "\n" +
			"    GROUP BY user_id\n" +
			")\n"
		out.from = "users u LEFT JOIN ec ON ec.user_id = u.id"
	}
	return out
}

// MembersQuery returns one page of matching users, ordered by user id.
// Paging uses the last id of the previous page ("keyset pagination") instead of OFFSET:
// OFFSET 100000 makes the database read and throw away 100,000 rows, a WHERE id > $x does not.
func (r Rules) MembersQuery(now time.Time, afterUserID string, limit int) Query {
	c := r.compile(now)
	sql := c.with +
		"SELECT u.id, u.last_seen_at\n" +
		"FROM " + c.from + "\n" +
		"WHERE (" + c.where + ")\n" +
		"  AND u.id > " + c.p.add(afterUserID) + "\n" +
		"ORDER BY u.id\n" +
		"LIMIT " + c.p.add(limit)
	return Query{SQL: sql, Args: c.p.args}
}

// CountQuery returns the number of matching users.
func (r Rules) CountQuery(now time.Time) Query {
	c := r.compile(now)
	sql := c.with +
		"SELECT count(*)\n" +
		"FROM " + c.from + "\n" +
		"WHERE " + c.where
	return Query{SQL: sql, Args: c.p.args}
}
