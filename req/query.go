package req

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// The queries assume a model without Validate errors; on an invalid model
// they skip what doesn't resolve instead of failing.

// TraceResult is everything reachable from Root, toward the origin (Up) and
// toward the solution (Down). Edges keep their stored direction.
type TraceResult struct {
	Root  Ref         `json:"root"`
	Up    []Edge      `json:"up"`
	Down  []Edge      `json:"down"`
	Depth map[Ref]int `json:"depth"` // steps from Root: negative up, positive down
}

// Trace follows the trace graph from id in both directions.
func (m *Model) Trace(id Ref) (TraceResult, error) {
	if !m.has(id) {
		return TraceResult{}, fmt.Errorf("%s does not exist", id)
	}
	g := m.index()
	depth := map[Ref]int{id: 0}
	down, _ := g.reachDepth([]Ref{id}, Down, nil, func(n Ref, d int) { depth[n] = d })
	up, _ := g.reachDepth([]Ref{id}, Up, nil, func(n Ref, d int) { depth[n] = -d })
	return TraceResult{Root: id, Up: up, Down: down, Depth: depth}, nil
}

// graphIndex holds the edges by endpoint.
type graphIndex struct {
	out, in map[Ref][]Edge
}

func (m *Model) index() graphIndex {
	g := graphIndex{out: map[Ref][]Edge{}, in: map[Ref][]Edge{}}
	for _, e := range m.Edges() {
		g.out[e.From] = append(g.out[e.From], e)
		g.in[e.To] = append(g.in[e.To], e)
	}
	return g
}

// reach walks from the start nodes in direction d: forward along edges of
// direction d and backward along edges of the opposite direction. It returns
// the edges crossed and the nodes reached, excluding the start nodes. With a
// non-nil keep, it doesn't step onto nodes for which keep is false.
func (g graphIndex) reach(start []Ref, d Dir, keep func(Ref) bool) ([]Edge, []Ref) {
	return g.reachDepth(start, d, keep, nil)
}

// reachDepth is reach that also reports each node's distance from the start.
func (g graphIndex) reachDepth(start []Ref, d Dir, keep func(Ref) bool, at func(Ref, int)) ([]Edge, []Ref) {
	opp := Down
	if d == Down {
		opp = Up
	}
	seen := map[Ref]bool{}
	dist := map[Ref]int{}
	for _, s := range start {
		seen[s] = true
	}
	var edges []Edge
	var nodes []Ref
	queue := slices.Clone(start)
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		step := func(e Edge, next Ref) {
			if keep != nil && !keep(next) {
				return
			}
			edges = append(edges, e)
			if !seen[next] {
				seen[next] = true
				dist[next] = dist[n] + 1
				if at != nil {
					at(next, dist[next])
				}
				nodes = append(nodes, next)
				queue = append(queue, next)
			}
		}
		for _, e := range g.out[n] {
			if e.Dir == d {
				step(e, e.To)
			}
		}
		for _, e := range g.in[n] {
			if e.Dir == opp {
				step(e, e.From)
			}
		}
	}
	sortRefs(nodes)
	return edges, nodes
}

// ImpactResult lists what must be revisited when an element changes or, for
// an assumption, turns out false.
type ImpactResult struct {
	Changed  Ref   `json:"changed"`
	Direct   []Ref `json:"direct"`   // elements that depend on it directly
	Affected []Ref `json:"affected"` // everything below the direct ones in the trace graph
}

// Impact answers "what depends on this element?". For an Assumption, the
// direct dependents are the elements that assume it, directly or in their
// rationale. For anything else, such as a StandardClause with a new edition,
// they are the elements one step below it in the trace graph.
func (m *Model) Impact(id Ref) (ImpactResult, error) {
	x, ok := m.elems[id]
	if !ok {
		return ImpactResult{}, fmt.Errorf("%s does not exist", id)
	}
	g := m.index()
	var direct []Ref
	if _, ok := x.(*Assumption); ok {
		for _, e := range m.Elems() {
			for _, l := range links(e) {
				if (l.field == "assumes" || l.field == "rationale.assumes") && slices.Contains(l.to, id) {
					direct = append(direct, Ref(e.base().ID))
					break
				}
			}
		}
	} else {
		seen := map[Ref]bool{}
		for _, e := range g.in[id] {
			if e.Dir == Up && !seen[e.From] {
				seen[e.From] = true
				direct = append(direct, e.From)
			}
		}
		for _, e := range g.out[id] {
			if e.Dir == Down && !seen[e.To] {
				seen[e.To] = true
				direct = append(direct, e.To)
			}
		}
		sortRefs(direct)
	}
	_, below := g.reach(append(direct, id), Down, nil)
	isDirect := map[Ref]bool{}
	for _, d := range direct {
		isDirect[d] = true
	}
	var affected []Ref
	for _, b := range below {
		if !isDirect[b] {
			affected = append(affected, b)
		}
	}
	return ImpactResult{Changed: id, Direct: direct, Affected: affected}, nil
}

// Evidence is the computed satisfaction and verification state of one
// requirement.
//
// A requirement is satisfied when it has a satisfaction claim, and verified
// when every claim is verified: the latest verification of the claimed design
// element for this requirement passed at the claimed version. Through
// refinement, it is also satisfied or verified when any requirement that
// refines it is, since the refined statement implies the original. Through
// decomposition, it is satisfied or verified when all its children are.
// Rejected and obsolete elements don't count.
type Evidence struct {
	Requirement Ref      `json:"requirement"`
	Satisfied   bool     `json:"satisfied"`
	Verified    bool     `json:"verified"`
	Reasons     []string `json:"reasons"`
}

// Evidence computes the evidence state of one requirement.
func (m *Model) Evidence(id Ref) (Evidence, error) {
	e, ok := m.elems[id]
	if !ok {
		return Evidence{}, fmt.Errorf("%s does not exist", id)
	}
	if _, ok := e.(*Requirement); !ok {
		return Evidence{}, fmt.Errorf("%s is a %s, not a Requirement", id, KindOf(e))
	}
	return newEvidencer(m).of(id), nil
}

type evidencer struct {
	m        *Model
	sats     map[Ref][]*Satisfaction // by requirement
	vers     map[Ref][]*Verification // by requirement
	refiners map[Ref][]Ref           // requirement -> requirements that refine it
	memo     map[Ref]Evidence
	busy     map[Ref]bool // cycle guard
}

func newEvidencer(m *Model) *evidencer {
	ev := &evidencer{
		m:        m,
		sats:     map[Ref][]*Satisfaction{},
		vers:     map[Ref][]*Verification{},
		refiners: map[Ref][]Ref{},
		memo:     map[Ref]Evidence{},
		busy:     map[Ref]bool{},
	}
	for _, s := range All[Satisfaction](m) {
		if Active(s) {
			ev.sats[s.Requirement] = append(ev.sats[s.Requirement], s)
		}
	}
	for _, v := range All[Verification](m) {
		if Active(v) {
			for _, r := range v.Verifies {
				ev.vers[r] = append(ev.vers[r], v)
			}
		}
	}
	for _, r := range All[Requirement](m) {
		if Active(r) {
			for _, o := range r.Refines {
				ev.refiners[o] = append(ev.refiners[o], Ref(r.ID))
			}
		}
	}
	return ev
}

func (ev *evidencer) of(id Ref) Evidence {
	if e, ok := ev.memo[id]; ok {
		return e
	}
	res := Evidence{Requirement: id}
	if ev.busy[id] {
		res.Reasons = []string{"cycle in children or refines"}
		return res
	}
	ev.busy[id] = true
	defer delete(ev.busy, id)
	r, _ := ev.m.elems[id].(*Requirement)

	// Own claims.
	sats := ev.sats[id]
	if len(sats) == 0 {
		res.Reasons = append(res.Reasons, "no own satisfaction claim")
	} else {
		res.Satisfied = true
		res.Verified = true
		for _, s := range sats {
			ok, why := ev.claimVerified(id, s)
			res.Verified = res.Verified && ok
			res.Reasons = append(res.Reasons, why)
		}
	}

	// Refinement: any refiner suffices.
	for _, o := range ev.refiners[id] {
		oe := ev.of(o)
		if oe.Satisfied && !res.Satisfied {
			res.Satisfied = true
			res.Reasons = append(res.Reasons, fmt.Sprintf("satisfied through refinement %s", o))
		}
		if oe.Verified && !res.Verified {
			res.Verified = true
			res.Reasons = append(res.Reasons, fmt.Sprintf("verified through refinement %s", o))
		}
	}
	if !res.Verified {
		for _, o := range ev.refiners[id] {
			res.Reasons = append(res.Reasons, fmt.Sprintf("refined by %s, which is not verified", o))
		}
	}

	// Decomposition: all active children needed.
	if r != nil {
		var kids []Ref
		for _, c := range r.Children {
			if ce, ok := ev.m.elems[c]; ok && Active(ce) {
				kids = append(kids, c)
			}
		}
		if len(kids) > 0 {
			allSat, allVer := true, true
			var unver []Ref
			for _, c := range kids {
				ce := ev.of(c)
				allSat = allSat && ce.Satisfied
				allVer = allVer && ce.Verified
				if !ce.Verified {
					unver = append(unver, c)
				}
			}
			if allSat && !res.Satisfied {
				res.Satisfied = true
				res.Reasons = append(res.Reasons, "satisfied through all children")
			}
			switch {
			case allVer && !res.Verified:
				res.Verified = true
				res.Reasons = append(res.Reasons, "verified through all children")
			case !allVer && !res.Verified:
				res.Reasons = append(res.Reasons, "children not verified: "+joinRefs(unver, ", "))
			}
		}
	}

	ev.memo[id] = res
	return res
}

// claimVerified checks one satisfaction claim against its latest verification.
func (ev *evidencer) claimVerified(req Ref, s *Satisfaction) (bool, string) {
	var latest *Verification
	for _, v := range ev.vers[req] {
		if v.Item != s.By {
			continue
		}
		if latest == nil || v.Date.After(latest.Date) || v.Date.Equal(latest.Date) && v.ID > latest.ID {
			latest = v
		}
	}
	switch {
	case latest == nil:
		return false, fmt.Sprintf("%s (%s at %s): no verification", s.ID, s.By, s.AtVersion)
	case latest.AtVersion != s.AtVersion:
		return false, fmt.Sprintf("%s (%s at %s): latest verification %s is at %s", s.ID, s.By, s.AtVersion, latest.ID, latest.AtVersion)
	case latest.Result != "pass":
		return false, fmt.Sprintf("%s (%s at %s): latest verification %s is %s", s.ID, s.By, s.AtVersion, latest.ID, latest.Result)
	}
	return true, fmt.Sprintf("%s (%s at %s): verified by %s", s.ID, s.By, s.AtVersion, latest.ID)
}

// Unverified returns the evidence of every active requirement that isn't
// verified. With a specification ID it looks only at the requirements that
// specification contains; with an empty ID, at all requirements.
func (m *Model) Unverified(spec Ref) ([]Evidence, error) {
	var scope []Ref
	if spec == "" {
		for _, r := range All[Requirement](m) {
			scope = append(scope, Ref(r.ID))
		}
	} else {
		e, ok := m.elems[spec]
		if !ok {
			return nil, fmt.Errorf("%s does not exist", spec)
		}
		s, ok := e.(*Specification)
		if !ok {
			return nil, fmt.Errorf("%s is a %s, not a Specification", spec, KindOf(e))
		}
		scope = slices.Clone(s.Contains)
		sortRefs(scope)
	}
	ev := newEvidencer(m)
	var out []Evidence
	for _, id := range scope {
		if e, ok := m.elems[id]; !ok || !Active(e) {
			continue
		}
		if x := ev.of(id); !x.Verified {
			out = append(out, x)
		}
	}
	return out, nil
}

// CoverageResult says whether an origin of goals is fully covered.
type CoverageResult struct {
	Origin       Ref        `json:"origin"`
	Goals        []Ref      `json:"goals"`
	Requirements []Evidence `json:"requirements"`
	Covered      bool       `json:"covered"`
	Reasons      []string   `json:"reasons,omitempty"`
}

// Coverage checks an origin of goals: a Need, Hazard, ThreatScenario or
// StandardClause. It is covered when at least one active goal addresses it,
// and every active requirement below those goals (through subgoals,
// operationalizes, children and refines) is verified. For a
// StandardClause this is the compliance check.
func (m *Model) Coverage(origin Ref) (CoverageResult, error) {
	e, ok := m.elems[origin]
	if !ok {
		return CoverageResult{}, fmt.Errorf("%s does not exist", origin)
	}
	switch e.(type) {
	case *Need, *Hazard, *ThreatScenario, *StandardClause:
	default:
		return CoverageResult{}, fmt.Errorf("%s is a %s; want a Need, Hazard, ThreatScenario or StandardClause", origin, KindOf(e))
	}
	res := CoverageResult{Origin: origin, Covered: true}
	// Only goals and requirements: requirements derived from a design
	// element belong to that design, not to every origin above it.
	goalOrReq := func(id Ref) bool {
		k := KindOf(m.elems[id])
		return k == KindGoal || k == KindRequirement
	}
	_, below := m.index().reach([]Ref{origin}, Down, goalOrReq)
	ev := newEvidencer(m)
	for _, id := range below {
		x := m.elems[id]
		if !Active(x) {
			continue
		}
		switch x.(type) {
		case *Goal:
			res.Goals = append(res.Goals, id)
		case *Requirement:
			xe := ev.of(id)
			res.Requirements = append(res.Requirements, xe)
			if !xe.Verified {
				res.Covered = false
				res.Reasons = append(res.Reasons, fmt.Sprintf("%s is not verified", id))
			}
		}
	}
	if len(res.Goals) == 0 {
		res.Covered = false
		res.Reasons = append(res.Reasons, "no active goal addresses it")
	} else if len(res.Requirements) == 0 {
		res.Covered = false
		res.Reasons = append(res.Reasons, "no active requirement below its goals")
	}
	return res, nil
}

// Gaps reports completeness problems: things a valid model may still lack.
// They are the checks the vocabulary document assigns to SHACL or SPARQL,
// done here because the model is a closed world.
func (m *Model) Gaps() []Issue {
	var is []Issue
	add := func(id Ref, field, format string, args ...any) {
		is = append(is, Issue{ID: id, Field: field, Message: fmt.Sprintf(format, args...)})
	}

	addressed := map[Ref]bool{}
	operationalized := map[Ref]bool{}
	isChild := map[Ref]bool{}
	refined := map[Ref]bool{}
	satisfiesSomething := map[Ref]bool{}
	selected := map[Ref][]Ref{} // goal -> selected solution concepts
	concepts := map[Ref]int{}   // goal -> number of active solution concepts

	for _, g := range All[Goal](m) {
		if Active(g) {
			for _, a := range g.Addresses {
				addressed[a] = true
			}
		}
	}
	for _, r := range All[Requirement](m) {
		if Active(r) {
			for _, g := range r.Operationalizes {
				operationalized[g] = true
			}
			for _, c := range r.Children {
				isChild[c] = true
			}
			for _, o := range r.Refines {
				refined[o] = true
			}
		}
	}
	for _, s := range All[Satisfaction](m) {
		if Active(s) {
			satisfiesSomething[s.By] = true
		}
	}
	for _, c := range All[SolutionConcept](m) {
		if Active(c) {
			for _, g := range c.Addresses {
				concepts[g]++
				if c.Selected {
					selected[g] = append(selected[g], Ref(c.ID))
				}
			}
		}
	}

	for _, e := range m.Elems() {
		if !Active(e) {
			continue
		}
		id := Ref(e.base().ID)
		if p := e.base().Provenance; p != nil && p.GeneratedBy != "" && e.base().Status == "approved" && p.ApprovedBy == "" {
			add(id, "provenance.approvedBy", "generated by %s and approved, but no approver is recorded", p.GeneratedBy)
		}
		switch e := e.(type) {
		case *Need, *Hazard, *ThreatScenario, *StandardClause:
			if !addressed[id] {
				add(id, "", "no active goal addresses it")
			}
		case *Goal:
			if len(e.Subgoals) == 0 && !operationalized[id] {
				add(id, "", "leaf goal with no requirement operationalizing it")
			}
			if concepts[id] > 0 && len(selected[id]) == 0 {
				add(id, "", "solution concepts address it, but none is selected")
			}
			if len(selected[id]) > 1 {
				add(id, "", "more than one selected solution concept addresses it: %s", joinRefs(selected[id], ", "))
			}
		case *Requirement:
			for _, rel := range []struct {
				field string
				to    []Ref
			}{{"children", e.Children}, {"refines", e.Refines}} {
				for _, o := range rel.to {
					if or, ok := m.elems[o].(*Requirement); ok && or.Context != e.Context {
						add(id, rel.field, "%s is on %s but %s is on %s; keep tool, product and training-data requirements apart", o, or.Context, id, e.Context)
					}
				}
			}
			if len(e.Criteria) == 0 && !refined[id] && len(e.Children) == 0 {
				add(id, "criteria", "no acceptance criterion, so it can't be verified")
			}
			if slices.Contains(e.Concerns, "safety") && e.Safety == nil {
				add(id, "safety", "safety concern without an ASIL")
			}
			if slices.Contains(e.Concerns, "security") && e.Security == nil {
				add(id, "security", "security concern without a CAL")
			}
			if len(e.Operationalizes) == 0 && !isChild[id] && len(e.Refines) == 0 && len(e.DerivedFrom) == 0 {
				add(id, "", "traces to no goal: not operationalizing, a child, refining or derived")
			}
			for _, d := range e.DerivedFrom {
				if m.has(d) && !satisfiesSomething[d] {
					add(id, "derivedFrom", "%s satisfies no requirement, so the derivation can't be traced or exported", d)
				}
			}
		}
	}
	return is
}

// Hit is one search result.
type Hit struct {
	ID   Ref    `json:"id"`
	Kind Kind   `json:"kind"`
	Name string `json:"name,omitempty"`
}

// Find returns the elements whose ID, name, description, text or statement
// contain every word of query, ignoring case.
func (m *Model) Find(query string) []Hit {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	var hits []Hit
	for _, e := range m.Elems() {
		b := e.base()
		hay := []string{b.ID, b.Name, b.Description}
		switch e := e.(type) {
		case *Idea:
			hay = append(hay, e.Statement)
		case *Need:
			hay = append(hay, e.Statement)
		case *Requirement:
			hay = append(hay, e.Text)
		case *Assumption:
			hay = append(hay, e.Text)
		case *StandardClause:
			hay = append(hay, e.Standard, e.Clause)
		}
		text := strings.ToLower(strings.Join(hay, "\n"))
		match := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				match = false
				break
			}
		}
		if match {
			hits = append(hits, Hit{ID: Ref(b.ID), Kind: KindOf(e), Name: b.Name})
		}
	}
	return hits
}

func sortRefs(rs []Ref) { sort.Slice(rs, func(i, j int) bool { return rs[i] < rs[j] }) }
