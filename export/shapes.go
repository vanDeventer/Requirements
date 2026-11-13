package export

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/vanDeventer/Requirements/req"
)

// ShapesOptions configure the SHACL export. Base and Vocab must match the
// Turtle export the shapes validate.
type ShapesOptions struct {
	Base  string
	Vocab string
	// Structure writes the structural rules as sh:Violation shapes: install
	// them in the store's shapes graph to guard every write.
	Structure bool
	// Gaps writes the completeness checks as sh:Warning shapes. Stores such
	// as GraphDB reject a transaction on warnings too, so run these on
	// demand (e.g. GraphDB's validate endpoint) instead of installing them.
	Gaps bool
}

// Shapes writes SHACL shapes for Turtle exports of models like m. The
// structural shapes restate the model tool's validation: required fields,
// reference kinds, concept values from m's schemes, the ID pattern and
// datatypes. The gap shapes restate its completeness checks, which a
// reasoner can't do because OWL assumes an open world.
//
// Only SHACL Core is used, without sequence paths or SPARQL constraints, so
// RDF4J-based stores such as GraphDB can run the shapes.
func Shapes(w io.Writer, m *req.Model, opt ShapesOptions) error {
	if !opt.Structure && !opt.Gaps {
		return fmt.Errorf("choose structure shapes, gap shapes or both")
	}
	if opt.Base == "" {
		opt.Base = DefaultBase
	}
	if opt.Vocab == "" {
		opt.Vocab = DefaultVocab
	}
	for _, iri := range []string{opt.Base, opt.Vocab} {
		if !validIRI(iri) {
			return fmt.Errorf("%q is not an absolute IRI", iri)
		}
	}
	x := &shapes{w: bufio.NewWriter(w), m: m, t: &turtle{base: opt.Base, m: m}}
	fmt.Fprintf(x.w, `@prefix req: <%s> .
@prefix rs: <%sshapes#> .
@prefix sh: <http://www.w3.org/ns/shacl#> .
@prefix prov: <http://www.w3.org/ns/prov#> .
@prefix dcterms: <http://purl.org/dc/terms/> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .

# Generated from the requirements model's schemes; regenerate when they change.

`, opt.Vocab, opt.Base)
	if opt.Structure {
		x.structure()
	}
	if opt.Gaps {
		x.gaps()
	}
	return x.w.Flush()
}

type shapes struct {
	w *bufio.Writer
	m *req.Model
	t *turtle
}

// prop is one sh:property constraint.
type prop struct {
	path     string
	min, max int // 0 means unconstrained
	kinds    []req.Kind
	scheme   string
	datatype string
	in       []string
	pattern  string
	node     string // IRI or blank
}

func (x *shapes) propText(p prop) string {
	var b strings.Builder
	b.WriteString("[ sh:path " + p.path)
	if p.min > 0 {
		fmt.Fprintf(&b, " ; sh:minCount %d", p.min)
	}
	if p.max > 0 {
		fmt.Fprintf(&b, " ; sh:maxCount %d", p.max)
	}
	switch len(p.kinds) {
	case 0:
	case 1:
		b.WriteString(" ; sh:class req:" + string(p.kinds[0]))
	default:
		var alts []string
		for _, k := range p.kinds {
			alts = append(alts, "[ sh:class req:"+string(k)+" ]")
		}
		b.WriteString(" ; sh:or ( " + strings.Join(alts, " ") + " )")
	}
	if p.scheme != "" {
		var vals []string
		if s, ok := x.m.Scheme(p.scheme); ok {
			for _, c := range s.Concepts {
				vals = append(vals, x.t.concept(p.scheme, req.Ref(c.ID)))
			}
		}
		b.WriteString(" ; sh:in ( " + strings.Join(vals, " ") + " )")
	}
	if p.datatype != "" {
		b.WriteString(" ; sh:datatype " + p.datatype)
	}
	if len(p.in) > 0 {
		b.WriteString(" ; sh:in ( " + strings.Join(p.in, " ") + " )")
	}
	if p.pattern != "" {
		b.WriteString(" ; sh:pattern " + lit(p.pattern))
	}
	if p.node != "" {
		b.WriteString(" ; sh:node " + p.node)
	}
	b.WriteString(" ]")
	return b.String()
}

func (x *shapes) shape(name, target, message, severity string, props []prop, extra ...string) {
	fmt.Fprintf(x.w, "rs:%s\n    a sh:NodeShape ;\n    %s ;\n    sh:severity %s ;\n    sh:message %s", name, target, severity, lit(message))
	for _, p := range props {
		fmt.Fprintf(x.w, " ;\n    sh:property %s", x.propText(p))
	}
	for _, e := range extra {
		fmt.Fprintf(x.w, " ;\n    %s", e)
	}
	fmt.Fprint(x.w, " .\n\n")
}

const idPattern = "^[A-Za-z][A-Za-z0-9-]*$"

func (x *shapes) structure() {
	// Every element: one well-formed ID, a status from the scheme, an owner.
	common := []prop{
		{path: "dcterms:identifier", min: 1, max: 1, datatype: "xsd:string", pattern: idPattern},
		{path: "rdfs:label", max: 1},
		{path: "req:status", max: 1, scheme: "status"},
		{path: "req:owner", max: 1, kinds: []req.Kind{req.KindStakeholder}},
		{path: "req:version", max: 1, datatype: "xsd:string"},
		{path: "req:rationale", max: 1, node: "rs:RationaleShape"},
		{path: "prov:wasAttributedTo", max: 1, kinds: []req.Kind{req.KindStakeholder}},
	}
	var targets []string
	for _, k := range req.Kinds() {
		targets = append(targets, "req:"+string(k))
	}
	x.shape("ElementShape", "sh:targetClass "+strings.Join(targets, " , "),
		"Every element has one well-formed ID, at most one status from the status scheme and at most one owner.", "sh:Violation", common)

	x.shape("RationaleShape", "sh:targetObjectsOf req:rationale", "A rationale's assumptions are Assumptions.", "sh:Violation", []prop{
		{path: "req:text", max: 1, datatype: "xsd:string"},
		{path: "req:assumes", kinds: []req.Kind{req.KindAssumption}},
	})

	k := func(ks ...req.Kind) []req.Kind { return ks }
	origins := k(req.KindNeed, req.KindHazard, req.KindThreatScenario, req.KindStandardClause)
	for _, s := range []struct {
		kind  req.Kind
		props []prop
	}{
		{req.KindStakeholder, []prop{{path: "req:role", max: 1, scheme: "role"}}},
		{req.KindIdea, []prop{
			{path: "req:statement", min: 1, max: 1, datatype: "xsd:string"},
			{path: "req:proposedBy", max: 1, kinds: k(req.KindStakeholder)},
		}},
		{req.KindNeed, []prop{
			{path: "req:statement", min: 1, max: 1, datatype: "xsd:string"},
			{path: "req:heldBy", kinds: k(req.KindStakeholder)},
			{path: "req:origin", kinds: k(req.KindIdea)},
		}},
		{req.KindGoal, []prop{
			{path: "req:pattern", max: 1, scheme: "goal-pattern"},
			{path: "req:soft", max: 1, datatype: "xsd:boolean"},
			{path: "req:refinement", max: 1, in: []string{`"AND"`, `"OR"`}},
			{path: "req:subgoal", kinds: k(req.KindGoal)},
			{path: "req:addresses", kinds: origins},
			{path: "req:assumes", kinds: k(req.KindAssumption)},
		}},
		{req.KindSolutionConcept, []prop{
			{path: "req:kind", max: 1, scheme: "concept-kind"},
			{path: "req:conceptFor", kinds: k(req.KindGoal)},
			{path: "req:selected", max: 1, datatype: "xsd:boolean"},
		}},
		{req.KindRequirement, []prop{
			{path: "req:text", min: 1, max: 1, datatype: "xsd:string"},
			{path: "req:context", min: 1, max: 1, scheme: "requirement-context"},
			{path: "req:form", max: 1, scheme: "form"},
			{path: "req:concern", scheme: "concern"},
			{path: "req:operationalizes", kinds: k(req.KindGoal)},
			{path: "req:presumes", max: 1, kinds: k(req.KindSolutionConcept)},
			{path: "req:child", kinds: k(req.KindRequirement)},
			{path: "req:refines", kinds: k(req.KindRequirement)},
			{path: "req:derivedFrom", kinds: k(req.KindDesignElement)},
			{path: "req:assumes", kinds: k(req.KindAssumption)},
			{path: "req:criterion", node: "rs:CriterionShape"},
			{path: "req:asil", max: 1, scheme: "asil"},
			{path: "req:cal", max: 1, scheme: "cal"},
			{path: "req:threatScenario", max: 1, kinds: k(req.KindThreatScenario)},
		}},
		{req.KindAssumption, []prop{
			{path: "req:text", min: 1, max: 1, datatype: "xsd:string"},
			{path: "req:kind", max: 1, scheme: "assumption-kind"},
		}},
		{req.KindStandardClause, []prop{
			{path: "req:standard", min: 1, max: 1, datatype: "xsd:string"},
			{path: "req:edition", min: 1, max: 1, datatype: "xsd:string"},
			{path: "req:clause", min: 1, max: 1, datatype: "xsd:string"},
		}},
		{req.KindSpecification, []prop{
			{path: "req:subject", max: 1, kinds: k(req.KindDesignElement)},
			{path: "req:contains", kinds: k(req.KindRequirement)},
		}},
		{req.KindDesignElement, []prop{
			{path: "req:kind", max: 1, scheme: "design-kind"},
			{path: "req:parent", max: 1, kinds: k(req.KindDesignElement)},
			{path: "req:realizes", max: 1, kinds: k(req.KindSolutionConcept)},
		}},
		{req.KindSatisfaction, []prop{
			{path: "req:requirement", min: 1, max: 1, kinds: k(req.KindRequirement)},
			{path: "req:by", min: 1, max: 1, kinds: k(req.KindDesignElement)},
			{path: "req:atVersion", min: 1, max: 1, datatype: "xsd:string"},
			{path: "dcterms:date", min: 1, max: 1, datatype: "xsd:dateTime"},
		}},
		{req.KindVerification, []prop{
			{path: "req:verifies", min: 1, kinds: k(req.KindRequirement)},
			{path: "req:item", min: 1, max: 1, kinds: k(req.KindDesignElement)},
			{path: "req:method", min: 1, max: 1, scheme: "verification-method"},
			{path: "req:result", min: 1, max: 1, scheme: "verdict"},
			{path: "req:atVersion", min: 1, max: 1, datatype: "xsd:string"},
			{path: "dcterms:date", min: 1, max: 1, datatype: "xsd:dateTime"},
		}},
	} {
		x.shape(string(s.kind)+"Shape", "sh:targetClass req:"+string(s.kind),
			fmt.Sprintf("A %s's fields have the allowed kinds, values and counts.", s.kind), "sh:Violation", s.props)
	}

	x.shape("CriterionShape", "sh:targetObjectsOf req:criterion", "An acceptance criterion has a metric from the scheme, an operator and a decimal threshold.", "sh:Violation", []prop{
		{path: "req:metric", min: 1, max: 1, scheme: "metric"},
		{path: "req:operator", min: 1, max: 1, in: []string{`"<"`, `"<="`, `"="`, `">="`, `">"`}},
		{path: "req:threshold", min: 1, max: 1, datatype: "xsd:decimal"},
	})
}

// inactive is the sh:or branch that exempts rejected and obsolete elements.
// The minCount matters: sh:in alone holds for an element without a status.
func (x *shapes) inactive() string {
	return fmt.Sprintf("[ sh:property [ sh:path req:status ; sh:minCount 1 ; sh:in ( %s %s ) ] ]",
		x.t.concept("status", "rejected"), x.t.concept("status", "obsolete"))
}

func has(path string) string { return "[ sh:property [ sh:path " + path + " ; sh:minCount 1 ] ]" }

func hasValue(path, value string) string {
	return "[ sh:property [ sh:path " + path + " ; sh:hasValue " + value + " ] ]"
}

func or(alts ...string) string { return "sh:or ( " + strings.Join(alts, " ") + " )" }

func (x *shapes) gaps() {
	const warn = "sh:Warning"
	x.shape("OriginAddressedShape", "sh:targetClass req:Need , req:Hazard , req:ThreatScenario , req:StandardClause",
		"No active goal addresses it.", warn, nil,
		or(x.inactive(), has("[ sh:inversePath req:addresses ]")))
	x.shape("LeafGoalShape", "sh:targetClass req:Goal",
		"Leaf goal with no requirement operationalizing it.", warn, nil,
		or(x.inactive(), has("req:subgoal"), has("[ sh:inversePath req:operationalizes ]")))
	x.shape("CriterionPresentShape", "sh:targetClass req:Requirement",
		"No acceptance criterion, so it can't be verified.", warn, nil,
		or(x.inactive(), has("req:criterion"), has("[ sh:inversePath req:refines ]"), has("req:child")))
	x.shape("TracesToGoalShape", "sh:targetClass req:Requirement",
		"Traces to no goal: not operationalizing, a child, refining or derived.", warn, nil,
		or(x.inactive(), has("req:operationalizes"), has("[ sh:inversePath req:child ]"), has("req:refines"), has("req:derivedFrom")))
	safety := x.t.concept("concern", "safety")
	security := x.t.concept("concern", "security")
	x.shape("SafetyASILShape", "sh:targetClass req:Requirement",
		"Safety concern without an ASIL.", warn, nil,
		or("[ sh:not "+hasValue("req:concern", safety)+" ]", has("req:asil")))
	x.shape("SecurityCALShape", "sh:targetClass req:Requirement",
		"Security concern without a CAL.", warn, nil,
		or("[ sh:not "+hasValue("req:concern", security)+" ]", has("req:cal")))
	x.shape("AIApprovalShape", "sh:targetSubjectsOf prov:wasGeneratedBy",
		"Generated by a model and approved, but no approver is recorded.", warn, nil,
		or("[ sh:not "+hasValue("req:status", x.t.concept("status", "approved"))+" ]", has("prov:wasAttributedTo")))
}
