// Package req holds the SDV requirements model: the element types, file
// storage, structural validation and the queries over the trace graph.
//
// The vocabulary and the reasoning behind each type are in
// "Requirements Vocabulary Semantics and Relationships.md" at the repository root.
package req

import (
	"sort"
	"time"
)

// Ref is the ID of another element, or of a concept in a concept scheme.
type Ref string

// Element is embedded in every model element.
type Element struct {
	ID          string      `json:"id"` // stable, matches [A-Za-z][A-Za-z0-9-]*, e.g. "REQ-042"
	Name        string      `json:"name,omitempty"`
	Description string      `json:"description,omitempty"`
	Owner       Ref         `json:"owner,omitempty"`  // Stakeholder
	Status      Ref         `json:"status,omitempty"` // lifecycle concept: proposed, approved, rejected, obsolete
	Version     string      `json:"version,omitempty"`
	Rationale   *Rationale  `json:"rationale,omitempty"`  // nil when none is recorded
	Provenance  *Provenance `json:"provenance,omitempty"` // nil when written directly in this model
}

func (e *Element) base() *Element { return e }

// Provenance records where an element's content came from and who accepted
// it: the lineage the EU AI Act's transparency obligations ask for, mapped to
// W3C PROV in the Turtle export.
type Provenance struct {
	Sources      []string  `json:"sources,omitempty"`      // URIs of documents the content was taken from
	GeneratedBy  string    `json:"generatedBy,omitempty"`  // model and version when AI-generated; empty when written by a person
	ApprovedBy   Ref       `json:"approvedBy,omitempty"`   // Stakeholder who accepted the content
	ApprovedOn   time.Time `json:"approvedOn,omitzero"`    // when it was accepted
	ExternalTool string    `json:"externalTool,omitempty"` // tool it was imported from: reqif, sphinx-needs, ...
	ExternalID   string    `json:"externalID,omitempty"`   // its ID in that tool, kept for re-import
}

// Rationale links an element to the reasoning behind it.
type Rationale struct {
	Text         string `json:"text,omitempty"`
	Assumes      []Ref  `json:"assumes,omitempty"`      // Assumption the reasoning depends on
	Alternatives []Ref  `json:"alternatives,omitempty"` // elements considered and not chosen
}

type Stakeholder struct {
	Element
	Role Ref `json:"role,omitempty"` // concept in scheme "role"
}

type Idea struct {
	Element
	Statement  string `json:"statement"`
	ProposedBy Ref    `json:"proposedBy,omitempty"` // Stakeholder
}

type Need struct {
	Element
	Statement string `json:"statement"`
	HeldBy    []Ref  `json:"heldBy,omitempty"` // Stakeholder
	Origin    []Ref  `json:"origin,omitempty"` // Idea
}

type Goal struct {
	Element
	Pattern    Ref    `json:"pattern,omitempty"`    // achieve, maintain, cease, avoid
	Soft       bool   `json:"soft,omitempty"`       // no clear-cut satisfaction, e.g. "usable"
	Refinement string `json:"refinement,omitempty"` // "AND" or "OR"; required when there are subgoals
	Subgoals   []Ref  `json:"subgoals,omitempty"`
	Addresses  []Ref  `json:"addresses,omitempty"` // Need, Hazard, ThreatScenario or StandardClause
	Assumes    []Ref  `json:"assumes,omitempty"`   // Assumption needed for the subgoals to imply the goal
}

// SolutionConcept is sense 2 of "concept". Sense 1 is a concept in a Scheme.
type SolutionConcept struct {
	Element
	Kind      Ref   `json:"kind,omitempty"`      // operational, system
	Addresses []Ref `json:"addresses,omitempty"` // Goal
	Selected  bool  `json:"selected,omitempty"`  // alternatives stay in the model with Selected false
}

type Requirement struct {
	Element
	Text            string                `json:"text"`
	Context         Ref                   `json:"context"`                   // what the requirement is on: tool, product or training-data
	Form            Ref                   `json:"form,omitempty"`            // functional, quality, constraint, interface
	Concerns        []Ref                 `json:"concerns,omitempty"`        // safety, security, privacy, performance, ...
	Operationalizes []Ref                 `json:"operationalizes,omitempty"` // Goal
	Concept         Ref                   `json:"concept,omitempty"`         // SolutionConcept the requirement presumes
	Children        []Ref                 `json:"children,omitempty"`        // decomposition
	Refines         []Ref                 `json:"refines,omitempty"`
	DerivedFrom     []Ref                 `json:"derivedFrom,omitempty"` // DesignElement whose choice caused this requirement
	Assumes         []Ref                 `json:"assumes,omitempty"`     // Assumption
	Criteria        []AcceptanceCriterion `json:"criteria,omitempty"`
	Safety          *SafetyAttrs          `json:"safety,omitempty"`   // nil unless a safety concern
	Security        *SecurityAttrs        `json:"security,omitempty"` // nil unless a security concern
}

type AcceptanceCriterion struct {
	Metric    Ref     `json:"metric"`   // concept in scheme "metric", e.g. install-time-p95
	Operator  string  `json:"operator"` // "<", "<=", "=", ">=", ">"
	Threshold float64 `json:"threshold"`
	Unit      string  `json:"unit,omitempty"`      // ISO 80000 symbol, e.g. "s"; empty for counts
	Condition string  `json:"condition,omitempty"` // context in which it applies
}

type Assumption struct {
	Element
	Kind Ref    `json:"kind,omitempty"` // domain-property or expectation
	Text string `json:"text"`
}

type SafetyAttrs struct {
	ASIL Ref `json:"asil"` // QM, A, B, C, D
}

type SecurityAttrs struct {
	CAL            Ref `json:"cal"` // CAL1 to CAL4
	ThreatScenario Ref `json:"threatScenario,omitempty"`
}

type Hazard struct {
	Element
	Source string `json:"source,omitempty"` // IRI into the HARA results
}

type ThreatScenario struct {
	Element
	Source string `json:"source,omitempty"` // IRI into the TARA results
}

type StandardClause struct {
	Element
	Standard string `json:"standard"` // e.g. "ISO 26262-3"
	Edition  string `json:"edition"`  // e.g. "2018"
	Clause   string `json:"clause"`   // e.g. "7"
}

type Specification struct {
	Element
	Subject  Ref   `json:"subject,omitempty"`  // DesignElement the specification is for
	Contains []Ref `json:"contains,omitempty"` // Requirement
}

type DesignElement struct {
	Element
	Kind     Ref `json:"kind,omitempty"` // system, subsystem, component, interface, ml-model, dataset, ...
	Parent   Ref `json:"parent,omitempty"`
	Realizes Ref `json:"realizes,omitempty"` // SolutionConcept
}

// Satisfaction is a claim. Element.Owner is the claimant.
type Satisfaction struct {
	Element
	Requirement Ref       `json:"requirement"`
	By          Ref       `json:"by"`        // DesignElement
	AtVersion   string    `json:"atVersion"` // version of By
	Date        time.Time `json:"date"`
}

// Verification is evidence for a claim. Element.Owner is who performed it.
type Verification struct {
	Element
	Verifies  []Ref     `json:"verifies"`           // Requirement
	Item      Ref       `json:"item"`               // DesignElement under verification
	Method    Ref       `json:"method"`             // test (narrower: sil-test, hil-test, vil-test, shadow-test), analysis, inspection, demonstration
	Result    Ref       `json:"result"`             // pass, fail, inconclusive, error (SysML VerdictKind)
	Evidence  []string  `json:"evidence,omitempty"` // URIs of reports, logs, test runs
	AtVersion string    `json:"atVersion"`          // version of Item
	Date      time.Time `json:"date"`
}

// Scheme is a SKOS concept scheme: the allowed values for fields such as
// Status, Form or ASIL. It is not an Element; its concepts have IDs local to
// the scheme.
type Scheme struct {
	ID       string    `json:"id"`
	Name     string    `json:"name,omitempty"`
	Concepts []Concept `json:"concepts"`
}

type Concept struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	Broader string `json:"broader,omitempty"` // parent concept in the same scheme
}

// Has reports whether the scheme contains a concept with the given ID.
func (s *Scheme) Has(id Ref) bool {
	for _, c := range s.Concepts {
		if Ref(c.ID) == id {
			return true
		}
	}
	return false
}

// Elem is implemented by pointers to every element type.
type Elem interface {
	base() *Element
}

// Base returns the Element embedded in e.
func Base(e Elem) *Element { return e.base() }

// Kind names an element type.
type Kind string

const (
	KindStakeholder     Kind = "Stakeholder"
	KindIdea            Kind = "Idea"
	KindNeed            Kind = "Need"
	KindGoal            Kind = "Goal"
	KindSolutionConcept Kind = "SolutionConcept"
	KindRequirement     Kind = "Requirement"
	KindAssumption      Kind = "Assumption"
	KindHazard          Kind = "Hazard"
	KindThreatScenario  Kind = "ThreatScenario"
	KindStandardClause  Kind = "StandardClause"
	KindSpecification   Kind = "Specification"
	KindDesignElement   Kind = "DesignElement"
	KindSatisfaction    Kind = "Satisfaction"
	KindVerification    Kind = "Verification"
)

// kindInfo ties a Kind to its directory and constructor.
type kindInfo struct {
	kind Kind
	dir  string
	make func() Elem
}

// kinds lists every element kind in load order.
var kinds = []kindInfo{
	{KindStakeholder, "stakeholders", func() Elem { return new(Stakeholder) }},
	{KindIdea, "ideas", func() Elem { return new(Idea) }},
	{KindNeed, "needs", func() Elem { return new(Need) }},
	{KindGoal, "goals", func() Elem { return new(Goal) }},
	{KindSolutionConcept, "solution-concepts", func() Elem { return new(SolutionConcept) }},
	{KindRequirement, "requirements", func() Elem { return new(Requirement) }},
	{KindAssumption, "assumptions", func() Elem { return new(Assumption) }},
	{KindHazard, "hazards", func() Elem { return new(Hazard) }},
	{KindThreatScenario, "threat-scenarios", func() Elem { return new(ThreatScenario) }},
	{KindStandardClause, "standard-clauses", func() Elem { return new(StandardClause) }},
	{KindSpecification, "specifications", func() Elem { return new(Specification) }},
	{KindDesignElement, "design-elements", func() Elem { return new(DesignElement) }},
	{KindSatisfaction, "satisfactions", func() Elem { return new(Satisfaction) }},
	{KindVerification, "verifications", func() Elem { return new(Verification) }},
}

// KindOf returns the kind of e.
func KindOf(e Elem) Kind {
	switch e.(type) {
	case *Stakeholder:
		return KindStakeholder
	case *Idea:
		return KindIdea
	case *Need:
		return KindNeed
	case *Goal:
		return KindGoal
	case *SolutionConcept:
		return KindSolutionConcept
	case *Requirement:
		return KindRequirement
	case *Assumption:
		return KindAssumption
	case *Hazard:
		return KindHazard
	case *ThreatScenario:
		return KindThreatScenario
	case *StandardClause:
		return KindStandardClause
	case *Specification:
		return KindSpecification
	case *DesignElement:
		return KindDesignElement
	case *Satisfaction:
		return KindSatisfaction
	case *Verification:
		return KindVerification
	}
	return ""
}

func infoFor(k Kind) (kindInfo, bool) {
	for _, ki := range kinds {
		if ki.kind == k {
			return ki, true
		}
	}
	return kindInfo{}, false
}

// Model is the whole requirements model held in memory.
type Model struct {
	elems   map[Ref]Elem
	schemes map[string]*Scheme
}

// NewModel returns an empty model.
func NewModel() *Model {
	return &Model{elems: map[Ref]Elem{}, schemes: map[string]*Scheme{}}
}

// Put adds e to the model, replacing any element with the same ID.
func (m *Model) Put(e Elem) { m.elems[Ref(e.base().ID)] = e }

// PutScheme adds s to the model, replacing any scheme with the same ID.
func (m *Model) PutScheme(s *Scheme) { m.schemes[s.ID] = s }

// Remove deletes the element with the given ID, if present.
func (m *Model) Remove(id Ref) { delete(m.elems, id) }

// Get returns the element with the given ID.
func (m *Model) Get(id Ref) (Elem, bool) {
	e, ok := m.elems[id]
	return e, ok
}

// Scheme returns the concept scheme with the given ID.
func (m *Model) Scheme(id string) (*Scheme, bool) {
	s, ok := m.schemes[id]
	return s, ok
}

// IDs returns the IDs of all elements, sorted.
func (m *Model) IDs() []Ref {
	ids := make([]Ref, 0, len(m.elems))
	for id := range m.elems {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Elems returns all elements, sorted by ID.
func (m *Model) Elems() []Elem {
	ids := m.IDs()
	es := make([]Elem, len(ids))
	for i, id := range ids {
		es[i] = m.elems[id]
	}
	return es
}

// All returns every element of type T, sorted by ID.
func All[T any, P interface {
	*T
	Elem
}](m *Model) []P {
	var out []P
	for _, e := range m.Elems() {
		if p, ok := e.(P); ok {
			out = append(out, p)
		}
	}
	return out
}

// Active reports whether an element's lifecycle status still counts:
// rejected and obsolete elements are kept for history but ignored by the
// completeness and evidence queries.
func Active(e Elem) bool {
	s := e.base().Status
	return s != "rejected" && s != "obsolete"
}
