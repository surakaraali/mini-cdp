// Package seed generates a realistic, repeatable fake shop dataset for performance measurements.
//
// The same Config and now always produce the same users and events (except event ids),
// so "before" and "after" measurements run on comparable data.
package seed

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"time"
)

// Config sets the size and shape of the dataset.
type Config struct {
	Users  int    // number of users
	Events int    // number of events
	Days   int    // events are spread over the last Days days
	Seed   uint64 // random seed; same seed, same data
}

// Event names, in the order used by Event.Name.
var EventNames = []string{"page_view", "product_view", "add_to_cart", "purchase"}

const (
	pageView = iota
	productView
	addToCart
	purchase
)

// User is one generated user.
type User struct {
	ID        string
	City      string // "" means the user has no city attribute
	Plan      string
	FirstSeen time.Time
	LastSeen  time.Time
}

// Event is one generated event, kept small because there are a million of them in memory.
type Event struct {
	User    int32 // index into Dataset.Users
	Name    uint8 // index into EventNames
	At      time.Time
	Product int32 // product number for product events
	Amount  int32 // price or revenue in kuruş (1/100 TL), so it is an exact integer
}

// Dataset is the generated data, events sorted by time like a real table that grows day by day.
type Dataset struct {
	Users  []User
	Events []Event
}

// weighted picks one of values; weights must add up to 100.
func weighted[T any](rng *rand.Rand, values []T, weights []int) T {
	n := rng.IntN(100)
	for i, w := range weights {
		if n < w {
			return values[i]
		}
		n -= w
	}
	return values[len(values)-1]
}

var (
	cities      = []string{"Istanbul", "Ankara", "Izmir", "Bursa", "Antalya", "Adana", "Konya", ""}
	cityWeights = []int{40, 15, 10, 8, 7, 5, 5, 10}
	plans       = []string{"free", "pro", "gold"}
	planWeights = []int{70, 25, 5}
	nameWeights = []int{55, 25, 13, 7} // page_view, product_view, add_to_cart, purchase
	nameIndexes = []uint8{pageView, productView, addToCart, purchase}
	categories  = []string{"shoes", "phones", "laptops", "books", "toys", "kitchen", "beauty", "sports"}
)

const (
	heavyShare   = 20 // percent of events made by heavy users
	heavyPercent = 1  // percent of users who are heavy users
)

// Generate builds the dataset. Events fall in (now - Days, now].
func Generate(cfg Config, now time.Time) Dataset {
	rng := rand.New(rand.NewPCG(cfg.Seed, cfg.Seed^0x9E3779B97F4A7C15))
	now = now.Truncate(time.Second)
	window := time.Duration(cfg.Days) * 24 * time.Hour

	users := make([]User, cfg.Users)
	for i := range users {
		users[i] = User{
			ID:   fmt.Sprintf("u_%06d", i+1),
			City: weighted(rng, cities, cityWeights),
			Plan: weighted(rng, plans, planWeights),
		}
	}

	// Real traffic is uneven: the first 1% of users ("heavy users") make 20% of the events.
	heavyUsers := max(1, cfg.Users*heavyPercent/100)
	events := make([]Event, cfg.Events)
	for i := range events {
		user := rng.IntN(cfg.Users)
		if rng.IntN(100) < heavyShare {
			user = rng.IntN(heavyUsers)
		}
		e := Event{
			User: int32(user),
			Name: weighted(rng, nameIndexes, nameWeights),
			At:   now.Add(-time.Duration(rng.Int64N(int64(window)))).Truncate(time.Second),
		}
		switch e.Name {
		case productView, addToCart:
			e.Product = int32(1 + rng.IntN(5000))
			e.Amount = int32(5_000 + rng.IntN(495_000)) // 50.00 to 5,000.00 TL
		case purchase:
			e.Amount = int32(5_000 + rng.IntN(995_000))
		}
		events[i] = e
	}
	slices.SortFunc(events, func(a, b Event) int { return a.At.Compare(b.At) })

	// first/last seen follow the events; a user without events gets the start of the window.
	for i := range users {
		users[i].FirstSeen, users[i].LastSeen = now.Add(-window), now.Add(-window)
	}
	seen := make([]bool, len(users))
	for _, e := range events {
		u := &users[e.User]
		if !seen[e.User] {
			u.FirstSeen = e.At
			seen[e.User] = true
		}
		u.LastSeen = e.At // events are sorted, so the last one wins
	}

	return Dataset{Users: users, Events: events}
}

// money formats kuruş as a JSON number with two decimals, e.g. 19990 -> 199.90.
func money(kurus int32) string {
	return fmt.Sprintf("%d.%02d", kurus/100, kurus%100)
}

// AttributesJSON is the user's attributes column.
func (u User) AttributesJSON() []byte {
	if u.City == "" {
		return fmt.Appendf(nil, `{"plan":%q}`, u.Plan)
	}
	return fmt.Appendf(nil, `{"city":%q,"plan":%q}`, u.City, u.Plan)
}

// PropertiesJSON is the event's properties column. seq makes order ids unique.
func (e Event) PropertiesJSON(seq int) []byte {
	category := categories[int(e.Product)%len(categories)]
	switch e.Name {
	case productView, addToCart:
		return fmt.Appendf(nil, `{"product_id":"p_%04d","category":%q,"price":%s}`, e.Product, category, money(e.Amount))
	case purchase:
		return fmt.Appendf(nil, `{"order_id":"o_%07d","revenue":%s}`, seq, money(e.Amount))
	default:
		return fmt.Appendf(nil, `{"path":"/category/%s"}`, categories[seq%len(categories)])
	}
}
