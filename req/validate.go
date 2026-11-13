package req

import (
	"fmt"
	"slices"
	"strings"
)

// ValidID reports whether id matches [A-Za-z][A-Za-z0-9-]*. Such IDs are
// IRI-safe as they stand, safe as file names, and become quoted SysML short
// names (<'REQ-042'>).
func ValidID(id string) bool {
	if id == "" {
		return false
	}
	for i, c := range id {
		letter := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
		switch {
		case letter:
		case i > 0 && (c >= '0' && c <= '9' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// Issue is one problem found in the model.
type Issue struct {
	ID      Ref    `json:"id"`              // element the issue is about
	Field   string `json:"field,omitempty"` // JSON field name, if specific
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Field == "" {
		return fmt.Sprintf("%s: %s", i.ID, i.Message)
	}
	return fmt.Sprintf("%s.%s: %s", i.ID, i.Field, i.Message)
}

// Validate checks that the model is structurally consistent: IDs are well
// formed, every reference resolves to an element of an allowed kind, concept
// values come from their scheme, required fields are set, and the
// subgoal, children, refines and design parent relations have no cycles.
// An empty result means the model can be queried and exported.
func (m *Model) Validate() []Issue {
	var is []Issue
	add := func(id Ref, field, format string, args ...any) {
		is = append(is, Issue{ID: id, Field: field, Message: fmt.Sprintf(format, args...)})
	}

	for _, s := range m.schemes {
		seen := map[string]bool{}
		for _, c := range s.Concepts {
			if !ValidID(c.ID) {
				add(Ref(s.ID), "concepts", "invalid concept id %q", c.ID)
			}
			if seen[c.ID] {
				add(Ref(s.ID), "concepts", "duplicate concept %q", c.ID)
			}
			seen[c.ID] = true
		}
		for _, c := range s.Concepts {
			if c.Broader != "" && !seen[c.Broader] {
				add(Ref(s.ID), "concepts", "concept %q: broader %q is not in the scheme", c.ID, c.Broader)
			}
		}
	}

	for _, e := range m.Elems() {
		id := Ref(e.base().ID)
		if !ValidID(string(id)) {
			add(id, "id", "must match [A-Za-z][A-Za-z0-9-]*")
		}
		for _, l := range links(e) {
			for _, to := range l.to {
				t, ok := m.elems[to]
				switch {
				case !ok:
					add(id, l.field, "%s does not exist", to)
				case l.kinds != nil && !slices.Contains(l.kinds, KindOf(t)):
					add(id, l.field, "%s is a %s, want %s", to, KindOf(t), kindList(l.kinds))
				case to == id:
					add(id, l.field, "refers to itself")
				}
			}
		}
		for _, f := range conceptFields(e) {
			if len(f.values) == 0 {
				continue
			}
			s, ok := m.schemes[f.scheme]
			if !ok {
				add(id, f.field, "scheme %q is not defined", f.scheme)
				continue
			}
			for _, v := range f.values {
				if !s.Has(v) {
					add(id, f.field, "%q is not in scheme %q", v, f.scheme)
				}
			}
		}
		for _, r := range required(e) {
			add(id, r, "is required")
		}
		switch e := e.(type) {
		case *Goal:
			switch {
			case len(e.Subgoals) > 0 && e.Refinement != "AND" && e.Refinement != "OR":
				add(id, "refinement", `must be "AND" or "OR" when there are subgoals`)
			case len(e.Subgoals) == 0 && e.Refinement != "":
				add(id, "refinement", "set without subgoals")
			}
		case *Requirement:
			for _, c := range e.Criteria {
				if !slices.Contains([]string{"<", "<=", "=", ">=", ">"}, c.Operator) {
					add(id, "criteria.operator", "%q is not one of <, <=, =, >=, >", c.Operator)
				}
			}
			if e.Safety != nil && !slices.Contains(e.Concerns, "safety") {
				add(id, "safety", `set without the "safety" concern`)
			}
			if e.Security != nil && !slices.Contains(e.Concerns, "security") {
				add(id, "security", `set without the "security" concern`)
			}
		}
	}

	is = append(is, m.cycles("subgoals", func(e Elem) []Ref {
		if g, ok := e.(*Goal); ok {
			return g.Subgoals
		}
		return nil
	})...)
	is = append(is, m.cycles("children", func(e Elem) []Ref {
		if r, ok := e.(*Requirement); ok {
			return r.Children
		}
		return nil
	})...)
	is = append(is, m.cycles("refines", func(e Elem) []Ref {
		if r, ok := e.(*Requirement); ok {
			return r.Refines
		}
		return nil
	})...)
	is = append(is, m.cycles("parent", func(e Elem) []Ref {
		if d, ok := e.(*DesignElement); ok {
			return one(d.Parent)
		}
		return nil
	})...)
	return is
}

// required lists the JSON names of required fields that are empty.
func required(e Elem) []string {
	var miss []string
	need := func(empty bool, field string) {
		if empty {
			miss = append(miss, field)
		}
	}
	switch e := e.(type) {
	case *Idea:
		need(e.Statement == "", "statement")
	case *Need:
		need(e.Statement == "", "statement")
	case *Requirement:
		need(strings.TrimSpace(e.Text) == "", "text")
		need(e.Context == "", "context")
		for _, c := range e.Criteria {
			need(c.Metric == "", "criteria.metric")
		}
		if e.Safety != nil {
			need(e.Safety.ASIL == "", "safety.asil")
		}
		if e.Security != nil {
			need(e.Security.CAL == "", "security.cal")
		}
	case *Assumption:
		need(e.Text == "", "text")
	case *StandardClause:
		need(e.Standard == "", "standard")
		need(e.Edition == "", "edition")
		need(e.Clause == "", "clause")
	case *Satisfaction:
		need(e.Requirement == "", "requirement")
		need(e.By == "", "by")
		need(e.AtVersion == "", "atVersion")
		need(e.Date.IsZero(), "date")
	case *Verification:
		need(len(e.Verifies) == 0, "verifies")
		need(e.Item == "", "item")
		need(e.Method == "", "method")
		need(e.Result == "", "result")
		need(e.AtVersion == "", "atVersion")
		need(e.Date.IsZero(), "date")
	}
	return miss
}

// cycles reports each cycle in the relation given by next, once.
func (m *Model) cycles(field string, next func(Elem) []Ref) []Issue {
	const (
		unseen = iota
		onPath
		done
	)
	state := map[Ref]int{}
	var is []Issue
	var path []Ref
	var visit func(id Ref)
	visit = func(id Ref) {
		state[id] = onPath
		path = append(path, id)
		if e, ok := m.elems[id]; ok {
			for _, n := range next(e) {
				switch state[n] {
				case unseen:
					if m.has(n) {
						visit(n)
					}
				case onPath:
					start := slices.Index(path, n)
					cyc := append(slices.Clone(path[start:]), n)
					is = append(is, Issue{ID: n, Field: field, Message: "cycle " + joinRefs(cyc, " → ")})
				}
			}
		}
		path = path[:len(path)-1]
		state[id] = done
	}
	for _, id := range m.IDs() {
		if state[id] == unseen {
			visit(id)
		}
	}
	return is
}

func kindList(ks []Kind) string {
	s := make([]string, len(ks))
	for i, k := range ks {
		s[i] = string(k)
	}
	return strings.Join(s, " or ")
}

func joinRefs(rs []Ref, sep string) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = string(r)
	}
	return strings.Join(s, sep)
}
