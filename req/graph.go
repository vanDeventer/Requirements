package req

import "sort"

// Dir says how a relation is traversed by Trace.
type Dir int

const (
	// Side relations are context (owner, assumptions, parts) and are not
	// followed by Trace.
	Side Dir = iota
	// Up relations point from an element toward its origin: the problem side
	// (requirement → goal → need → idea).
	Up
	// Down relations point from an element toward what elaborates it: the
	// solution side (goal → subgoal, requirement → child).
	Down
)

// link is one reference field of an element, as stored.
type link struct {
	field string
	to    []Ref
	kinds []Kind // allowed target kinds; nil means any element
	dir   Dir
}

func one(r Ref) []Ref {
	if r == "" {
		return nil
	}
	return []Ref{r}
}

// links lists every reference field of e. It is the one place that knows
// which fields point where; validation and the trace graph both use it.
func links(e Elem) []link {
	b := e.base()
	ls := []link{{field: "owner", to: one(b.Owner), kinds: []Kind{KindStakeholder}, dir: Side}}
	if r := b.Rationale; r != nil {
		ls = append(ls,
			link{field: "rationale.assumes", to: r.Assumes, kinds: []Kind{KindAssumption}, dir: Side},
			link{field: "rationale.alternatives", to: r.Alternatives, dir: Side},
		)
	}
	if p := b.Provenance; p != nil {
		ls = append(ls, link{field: "provenance.approvedBy", to: one(p.ApprovedBy), kinds: []Kind{KindStakeholder}, dir: Side})
	}
	switch e := e.(type) {
	case *Idea:
		ls = append(ls, link{"proposedBy", one(e.ProposedBy), []Kind{KindStakeholder}, Side})
	case *Need:
		ls = append(ls,
			link{"heldBy", e.HeldBy, []Kind{KindStakeholder}, Side},
			link{"origin", e.Origin, []Kind{KindIdea}, Up},
		)
	case *Goal:
		ls = append(ls,
			link{"subgoals", e.Subgoals, []Kind{KindGoal}, Down},
			link{"addresses", e.Addresses, []Kind{KindNeed, KindHazard, KindThreatScenario, KindStandardClause}, Up},
			link{"assumes", e.Assumes, []Kind{KindAssumption}, Side},
		)
	case *SolutionConcept:
		ls = append(ls, link{"addresses", e.Addresses, []Kind{KindGoal}, Up})
	case *Requirement:
		ls = append(ls,
			link{"operationalizes", e.Operationalizes, []Kind{KindGoal}, Up},
			link{"concept", one(e.Concept), []Kind{KindSolutionConcept}, Up},
			link{"children", e.Children, []Kind{KindRequirement}, Down},
			link{"refines", e.Refines, []Kind{KindRequirement}, Up},
			link{"derivedFrom", e.DerivedFrom, []Kind{KindDesignElement}, Up},
			link{"assumes", e.Assumes, []Kind{KindAssumption}, Side},
		)
		if e.Security != nil {
			ls = append(ls, link{"security.threatScenario", one(e.Security.ThreatScenario), []Kind{KindThreatScenario}, Up})
		}
	case *Specification:
		ls = append(ls,
			link{"subject", one(e.Subject), []Kind{KindDesignElement}, Side},
			link{"contains", e.Contains, []Kind{KindRequirement}, Side},
		)
	case *DesignElement:
		ls = append(ls,
			link{"parent", one(e.Parent), []Kind{KindDesignElement}, Side},
			link{"realizes", one(e.Realizes), []Kind{KindSolutionConcept}, Up},
		)
	case *Satisfaction:
		// Traced as a DesignElement --satisfies--> Requirement edge; see Edges.
		ls = append(ls,
			link{"requirement", one(e.Requirement), []Kind{KindRequirement}, Side},
			link{"by", one(e.By), []Kind{KindDesignElement}, Side},
		)
	case *Verification:
		ls = append(ls,
			link{"verifies", e.Verifies, []Kind{KindRequirement}, Up},
			link{"item", one(e.Item), []Kind{KindDesignElement}, Side},
		)
	}
	return ls
}

// conceptField is a field whose values come from a concept scheme.
type conceptField struct {
	field  string
	scheme string
	values []Ref
}

func conceptFields(e Elem) []conceptField {
	fs := []conceptField{{"status", "status", one(e.base().Status)}}
	switch e := e.(type) {
	case *Stakeholder:
		fs = append(fs, conceptField{"role", "role", one(e.Role)})
	case *Goal:
		fs = append(fs, conceptField{"pattern", "goal-pattern", one(e.Pattern)})
	case *SolutionConcept:
		fs = append(fs, conceptField{"kind", "concept-kind", one(e.Kind)})
	case *Requirement:
		fs = append(fs,
			conceptField{"context", "requirement-context", one(e.Context)},
			conceptField{"form", "form", one(e.Form)},
			conceptField{"concerns", "concern", e.Concerns},
		)
		for _, c := range e.Criteria {
			fs = append(fs, conceptField{"criteria.metric", "metric", one(c.Metric)})
		}
		if e.Safety != nil {
			fs = append(fs, conceptField{"safety.asil", "asil", one(e.Safety.ASIL)})
		}
		if e.Security != nil {
			fs = append(fs, conceptField{"security.cal", "cal", one(e.Security.CAL)})
		}
	case *Assumption:
		fs = append(fs, conceptField{"kind", "assumption-kind", one(e.Kind)})
	case *DesignElement:
		fs = append(fs, conceptField{"kind", "design-kind", one(e.Kind)})
	case *Verification:
		fs = append(fs,
			conceptField{"method", "verification-method", one(e.Method)},
			conceptField{"result", "verdict", one(e.Result)},
		)
	}
	return fs
}

// Edge is one traversable relation in the trace graph.
type Edge struct {
	From Ref    `json:"from"`
	Rel  string `json:"rel"`
	To   Ref    `json:"to"`
	Via  Ref    `json:"via,omitempty"` // the Satisfaction behind a "satisfies" edge
	Dir  Dir    `json:"-"`
}

// Edges returns the Up and Down edges of the model, sorted. Edges to IDs that
// don't resolve are left out; Validate reports them.
func (m *Model) Edges() []Edge {
	var es []Edge
	for _, e := range m.Elems() {
		from := Ref(e.base().ID)
		if s, ok := e.(*Satisfaction); ok {
			if m.has(s.By) && m.has(s.Requirement) {
				es = append(es, Edge{From: s.By, Rel: "satisfies", To: s.Requirement, Via: from, Dir: Up})
			}
			continue
		}
		for _, l := range links(e) {
			if l.dir == Side {
				continue
			}
			for _, to := range l.to {
				if m.has(to) {
					es = append(es, Edge{From: from, Rel: l.field, To: to, Dir: l.dir})
				}
			}
		}
	}
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.Rel != b.Rel {
			return a.Rel < b.Rel
		}
		return a.To < b.To
	})
	return es
}

func (m *Model) has(id Ref) bool { _, ok := m.elems[id]; return ok }
