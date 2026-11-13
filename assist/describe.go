package assist

import (
	"github.com/vanDeventer/Requirements/req"
)

// describe lists a kind's fields with their types, help, reference kinds
// and allowed concept values, so a language model can write a valid element
// without guessing field names. The web form uses the same descriptions.
func describe(m *req.Model, k req.Kind) map[string]any {
	notes := []string{"Every element has an id matching [A-Za-z][A-Za-z0-9-]*, unique in the model. Dates are RFC 3339, e.g. 2026-11-13T09:00:00Z."}
	if k == req.KindRequirement {
		notes = append(notes,
			"A requirement is verifiable only with criteria: each has a metric (a concept), an operator (<, <=, =, >=, >), a numeric threshold and optionally a unit and condition.",
			"safety needs the concern safety, and security needs the concern security.",
			"A metric that isn't in the list must first be added to the metric scheme by a person.")
	}
	return map[string]any{"kind": k, "suggestedID": req.NextID(m, k), "fields": req.Fields(m, k), "notes": notes}
}

// fieldNames lists a kind's top-level JSON fields.
func fieldNames(k req.Kind) []string {
	var names []string
	for _, f := range req.Fields(req.NewModel(), k) {
		names = append(names, f.Field)
	}
	return names
}
