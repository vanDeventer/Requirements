// Package export writes a requirements model as RDF Turtle and as SysML v2
// textual notation. Both are generated from the Go model, which stays the
// source of truth, and both refuse a model with structural errors.
package export

import (
	"bufio"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

// InvalidError is returned for a model with structural errors.
type InvalidError struct{ Issues []req.Issue }

func (e *InvalidError) Error() string {
	return fmt.Sprintf("the model has %d structural errors; run check", len(e.Issues))
}

func checkValid(m *req.Model) error {
	if is := m.Validate(); len(is) > 0 {
		return &InvalidError{is}
	}
	return nil
}

const (
	// DefaultBase is the IRI prefix for elements when none is given. Set your
	// own: IRIs should be stable and owned by you.
	DefaultBase = "https://example.org/requirements/"
	// DefaultVocab is the namespace of the req: vocabulary.
	DefaultVocab = "https://github.com/vanDeventer/Requirements/vocab#"
)

// TurtleOptions configure the Turtle export.
type TurtleOptions struct {
	Base  string // IRI prefix for elements and concepts, ending in / or #
	Vocab string // namespace of the req: vocabulary

	// Computed adds the verdicts the model tool computes: req:isSatisfied
	// and req:isVerified on active requirements, req:isCovered on active
	// needs, hazards, threat scenarios and clauses.
	Computed bool
}

// Turtle writes the model as RDF Turtle. Element IRIs are Base+ID; concept
// IRIs are Base+"scheme/"+scheme+"/"+concept.
func Turtle(w io.Writer, m *req.Model, opt TurtleOptions) error {
	if err := checkValid(m); err != nil {
		return err
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
	t := &turtle{w: bufio.NewWriter(w), base: opt.Base, m: m, computed: opt.Computed}
	t.header(opt.Vocab)
	for _, s := range m.Schemes() {
		t.scheme(s)
	}
	for _, e := range m.Elems() {
		t.element(e)
	}
	return t.w.Flush()
}

type turtle struct {
	w        *bufio.Writer
	base     string
	m        *req.Model
	computed bool
}

type po struct {
	p    string
	objs []string
}

// props collects predicate-object pairs, skipping empty objects.
type props []po

func (ps *props) add(p string, objs ...string) {
	var keep []string
	for _, o := range objs {
		if o != "" {
			keep = append(keep, o)
		}
	}
	if len(keep) > 0 {
		*ps = append(*ps, po{p, keep})
	}
}

func (t *turtle) subject(s string, ps props) {
	fmt.Fprintf(t.w, "%s", s)
	for i, p := range ps {
		sep := " ;\n    "
		if i == 0 {
			sep = "\n    "
		}
		fmt.Fprintf(t.w, "%s%s %s", sep, p.p, strings.Join(p.objs, " , "))
	}
	fmt.Fprint(t.w, " .\n\n")
}

// blank renders props as an inline blank node.
func blank(ps props) string {
	var b strings.Builder
	b.WriteString("[ ")
	for i, p := range ps {
		if i > 0 {
			b.WriteString(" ; ")
		}
		b.WriteString(p.p + " " + strings.Join(p.objs, " , "))
	}
	b.WriteString(" ]")
	return b.String()
}

func (t *turtle) header(vocab string) {
	fmt.Fprintf(t.w, `@prefix : <%s> .
@prefix req: <%s> .
@prefix oslc_rm: <http://open-services.net/ns/rm#> .
@prefix prov: <http://www.w3.org/ns/prov#> .
@prefix skos: <http://www.w3.org/2004/02/skos/core#> .
@prefix dcterms: <http://purl.org/dc/terms/> .
@prefix rdfs: <http://www.w3.org/2000/01/rdf-schema#> .
@prefix xsd: <http://www.w3.org/2001/XMLSchema#> .

# Generated from the requirements model; edit the model, not this file.
# The req: vocabulary (OWL 2 RL) is published separately: req export ontology.

`, t.base, vocab)
}

func (t *turtle) scheme(s *req.Scheme) {
	iri := t.schemeIRI(s.ID)
	ps := props{{"a", []string{"skos:ConceptScheme"}}}
	ps.add("skos:prefLabel", lit(s.Name))
	t.subject(iri, ps)
	for _, c := range s.Concepts {
		label := c.Label
		if label == "" {
			label = c.ID
		}
		ps := props{{"a", []string{"skos:Concept"}}, {"skos:inScheme", []string{iri}}}
		ps.add("skos:notation", lit(c.ID))
		ps.add("skos:prefLabel", lit(label))
		if c.Broader != "" {
			ps.add("skos:broader", t.concept(s.ID, req.Ref(c.Broader)))
		}
		t.subject(t.concept(s.ID, req.Ref(c.ID)), ps)
	}
}

func (t *turtle) schemeIRI(scheme string) string { return "<" + t.base + "scheme/" + scheme + ">" }

func (t *turtle) concept(scheme string, v req.Ref) string {
	if v == "" {
		return ""
	}
	return "<" + t.base + "scheme/" + scheme + "/" + string(v) + ">"
}

func (t *turtle) concepts(scheme string, vs []req.Ref) []string {
	var out []string
	for _, v := range vs {
		out = append(out, t.concept(scheme, v))
	}
	return out
}

func (t *turtle) ref(id req.Ref) string {
	if id == "" {
		return ""
	}
	return ":" + string(id)
}

func (t *turtle) refs(ids []req.Ref) []string {
	var out []string
	for _, id := range ids {
		out = append(out, t.ref(id))
	}
	return out
}

func (t *turtle) element(e req.Elem) {
	b := req.Base(e)
	ps := props{{"a", []string{"req:" + string(req.KindOf(e))}}}
	ps.add("rdfs:label", lit(b.Name))
	ps.add("dcterms:identifier", lit(b.ID))
	ps.add("dcterms:description", lit(b.Description))
	ps.add("req:owner", t.ref(b.Owner))
	ps.add("req:status", t.concept("status", b.Status))
	ps.add("req:version", lit(b.Version))
	if r := b.Rationale; r != nil {
		var rp props
		rp.add("req:text", lit(r.Text))
		rp.add("req:assumes", t.refs(r.Assumes)...)
		rp.add("req:alternative", t.refs(r.Alternatives)...)
		if len(rp) > 0 {
			ps.add("req:rationale", blank(rp))
		}
	}
	if p := b.Provenance; p != nil {
		for _, s := range p.Sources {
			if validIRI(s) {
				ps.add("prov:wasDerivedFrom", "<"+s+">")
			} else {
				ps.add("req:sourceNote", lit(s))
			}
		}
		if p.GeneratedBy != "" {
			ps.add("prov:wasGeneratedBy", blank(props{
				{"a", []string{"prov:Activity"}},
				{"prov:wasAssociatedWith", []string{blank(props{{"a", []string{"prov:SoftwareAgent"}}, {"rdfs:label", []string{lit(p.GeneratedBy)}}})}},
			}))
		}
		ps.add("prov:wasAttributedTo", t.ref(p.ApprovedBy))
		ps.add("req:approvedOn", dateTime(p.ApprovedOn))
		ps.add("req:externalTool", lit(p.ExternalTool))
		ps.add("req:externalID", lit(p.ExternalID))
	}

	var extra []string // further statements about other subjects
	switch e := e.(type) {
	case *req.Stakeholder:
		ps.add("req:role", t.concept("role", e.Role))
	case *req.Idea:
		ps.add("req:statement", lit(e.Statement))
		ps.add("req:proposedBy", t.ref(e.ProposedBy))
	case *req.Need:
		ps.add("req:statement", lit(e.Statement))
		ps.add("req:heldBy", t.refs(e.HeldBy)...)
		ps.add("req:origin", t.refs(e.Origin)...)
		t.covered(&ps, e)
	case *req.Goal:
		ps.add("req:pattern", t.concept("goal-pattern", e.Pattern))
		if e.Soft {
			ps.add("req:soft", "true")
		}
		ps.add("req:refinement", lit(e.Refinement))
		ps.add("req:subgoal", t.refs(e.Subgoals)...)
		ps.add("req:addresses", t.refs(e.Addresses)...)
		ps.add("req:assumes", t.refs(e.Assumes)...)
	case *req.SolutionConcept:
		ps.add("req:kind", t.concept("concept-kind", e.Kind))
		ps.add("req:conceptFor", t.refs(e.Addresses)...)
		ps.add("req:origin", t.refs(e.Origin)...)
		ps.add("req:selected", strconv.FormatBool(e.Selected))
	case *req.Requirement:
		ps.add("req:text", lit(e.Text))
		ps.add("req:context", t.concept("requirement-context", e.Context))
		ps.add("req:form", t.concept("form", e.Form))
		ps.add("req:concern", t.concepts("concern", e.Concerns)...)
		ps.add("req:operationalizes", t.refs(e.Operationalizes)...)
		ps.add("req:presumes", t.ref(e.Concept))
		ps.add("req:child", t.refs(e.Children)...)
		ps.add("req:refines", t.refs(e.Refines)...)
		ps.add("req:derivedFrom", t.refs(e.DerivedFrom)...)
		ps.add("req:assumes", t.refs(e.Assumes)...)
		for _, c := range e.Criteria {
			var cp props
			cp.add("req:metric", t.concept("metric", c.Metric))
			cp.add("req:operator", lit(c.Operator))
			cp.add("req:threshold", decimal(c.Threshold))
			cp.add("req:unit", lit(c.Unit))
			cp.add("req:condition", lit(c.Condition))
			ps.add("req:criterion", blank(cp))
		}
		if e.Safety != nil {
			ps.add("req:asil", t.concept("asil", e.Safety.ASIL))
		}
		if e.Security != nil {
			ps.add("req:cal", t.concept("cal", e.Security.CAL))
			ps.add("req:threatScenario", t.ref(e.Security.ThreatScenario))
		}
		if t.computed && req.Active(e) {
			if ev, err := t.m.Evidence(req.Ref(e.ID)); err == nil {
				ps.add("req:isSatisfied", strconv.FormatBool(ev.Satisfied))
				ps.add("req:isVerified", strconv.FormatBool(ev.Verified))
			}
		}
	case *req.Assumption:
		ps.add("req:kind", t.concept("assumption-kind", e.Kind))
		ps.add("req:text", lit(e.Text))
	case *req.Hazard:
		ps.add("req:source", iriOrLit(e.Source))
		t.covered(&ps, e)
	case *req.ThreatScenario:
		ps.add("req:source", iriOrLit(e.Source))
		t.covered(&ps, e)
	case *req.StandardClause:
		ps.add("req:standard", lit(e.Standard))
		ps.add("req:edition", lit(e.Edition))
		ps.add("req:clause", lit(e.Clause))
		t.covered(&ps, e)
	case *req.Specification:
		ps.add("req:subject", t.ref(e.Subject))
		ps.add("req:contains", t.refs(e.Contains)...)
	case *req.DesignElement:
		ps.add("req:kind", t.concept("design-kind", e.Kind))
		ps.add("req:parent", t.ref(e.Parent))
		ps.add("req:realizes", t.ref(e.Realizes))
	case *req.Satisfaction:
		ps.add("req:requirement", t.ref(e.Requirement))
		ps.add("req:by", t.ref(e.By))
		ps.add("req:atVersion", lit(e.AtVersion))
		ps.add("dcterms:date", dateTime(e.Date))
		if req.Active(e) {
			// The direct link keeps simple queries simple.
			extra = append(extra, fmt.Sprintf("%s oslc_rm:satisfiedBy %s .\n\n", t.ref(e.Requirement), t.ref(e.By)))
		}
	case *req.Verification:
		ps.add("req:verifies", t.refs(e.Verifies)...)
		ps.add("req:item", t.ref(e.Item))
		ps.add("req:method", t.concept("verification-method", e.Method))
		ps.add("req:result", t.concept("verdict", e.Result))
		for _, ev := range e.Evidence {
			ps.add("req:evidence", iriOrLit(ev))
		}
		ps.add("req:atVersion", lit(e.AtVersion))
		ps.add("dcterms:date", dateTime(e.Date))
	}
	t.subject(t.ref(req.Ref(b.ID)), ps)
	for _, x := range extra {
		t.w.WriteString(x)
	}
}

// covered adds the computed coverage of an origin of goals.
func (t *turtle) covered(ps *props, e req.Elem) {
	if !t.computed || !req.Active(e) {
		return
	}
	if c, err := t.m.Coverage(req.Ref(req.Base(e).ID)); err == nil {
		ps.add("req:isCovered", strconv.FormatBool(c.Covered))
	}
}

// lit is a Turtle string literal, or "" for an empty string.
func lit(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, c := range s {
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\u%04X`, c)
			} else {
				b.WriteRune(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func decimal(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	return `"` + s + `"^^xsd:decimal`
}

func dateTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return `"` + t.UTC().Format(time.RFC3339) + `"^^xsd:dateTime`
}

// iriOrLit writes an absolute IRI as an IRI, and anything else as a string.
func iriOrLit(s string) string {
	if s == "" {
		return ""
	}
	if validIRI(s) {
		return "<" + s + ">"
	}
	return lit(s)
}

// validIRI reports whether s is an absolute IRI that can be written as a
// Turtle IRIREF without escapes.
func validIRI(s string) bool {
	if strings.ContainsAny(s, "<>\"{}|^`\\ \t\n\r") {
		return false
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && (u.Host != "" || u.Opaque != "")
}
