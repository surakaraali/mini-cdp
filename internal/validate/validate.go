// Package validate collects field-level problems found in a request body.
package validate

// FieldError is one problem with one field, for example
//
//	{"field": "events[2].name", "problem": "is required"}
type FieldError struct {
	Field   string `json:"field"`
	Problem string `json:"problem"`
}

// Errors is a list of problems. The zero value is an empty list, ready to use.
type Errors []FieldError

// Add records a problem.
func (e *Errors) Add(field, problem string) {
	*e = append(*e, FieldError{Field: field, Problem: problem})
}

// WithPrefix returns a copy where every field name starts with prefix,
// so "name" becomes "events[3].name" when checking item 3 of a batch.
func (e Errors) WithPrefix(prefix string) Errors {
	out := make(Errors, len(e))
	for i, fe := range e {
		out[i] = FieldError{Field: prefix + fe.Field, Problem: fe.Problem}
	}
	return out
}
