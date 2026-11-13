package export

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

// SysMLOptions configure the SysML v2 export.
type SysMLOptions struct {
	Package string // name of the top-level package; default "Requirements"
}

// SysML writes the model in SysML v2 textual notation, for reading and
// analysis in SysML v2 tools. It is export-only: edits to the file don't
// flow back.
//
// Element IDs become short names (<'REQ-042'>) and names become declared
// names. Standard constructs carry what SysML has words for: requirement,
// concern, satisfy, verification with a verify objective, #refinement
// dependencies, #derivation connections and assume constraints. The model's
// other relations (subgoal, operationalizes, addresses, ...) become
// dependencies tagged with metadata defined in the generated Vocabulary
// package, and concept schemes become enum defs.
//
// Rejected and obsolete satisfaction claims are left out, since satisfy
// asserts the claim.
func SysML(w io.Writer, m *req.Model, opt SysMLOptions) error {
	if err := checkValid(m); err != nil {
		return err
	}
	if opt.Package == "" {
		opt.Package = "Requirements"
	}
	x := &sysml{m: m, w: bufio.NewWriter(w), paths: map[req.Ref]string{}}
	if err := x.derivations(); err != nil {
		return err
	}
	x.designPaths()

	x.line(0, "// Generated from the requirements model; edit the model, not this file.")
	x.line(0, "package "+quote(opt.Package)+" {")
	x.line(1, "private import ScalarValues::*;")
	x.line(1, "private import ModelingMetadata::*;")
	x.line(1, "private import RequirementDerivation::*;")
	x.line(1, "private import Vocabulary::*;")
	x.blank()
	x.vocabulary()
	x.elements()
	x.relations()
	x.line(0, "}")
	return x.w.Flush()
}

type sysml struct {
	m       *req.Model
	w       *bufio.Writer
	paths   map[req.Ref]string // design element -> qualified name
	derived [][2]req.Ref       // original, derived requirement
}

func (x *sysml) line(indent int, s string) {
	x.w.WriteString(strings.Repeat("    ", indent) + s + "\n")
}

func (x *sysml) blank() { x.w.WriteString("\n") }

// derivations resolves DerivedFrom: a requirement derived from a design
// element is derived from each requirement that element satisfies.
func (x *sysml) derivations() error {
	satisfied := map[req.Ref][]req.Ref{} // design element -> requirements
	for _, s := range req.All[req.Satisfaction](x.m) {
		if req.Active(s) {
			satisfied[s.By] = append(satisfied[s.By], s.Requirement)
		}
	}
	var missing []string
	for _, r := range req.All[req.Requirement](x.m) {
		for _, d := range r.DerivedFrom {
			if len(satisfied[d]) == 0 {
				missing = append(missing, fmt.Sprintf("%s is derived from %s, which satisfies no requirement", r.ID, d))
				continue
			}
			for _, o := range satisfied[d] {
				x.derived = append(x.derived, [2]req.Ref{o, req.Ref(r.ID)})
			}
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("SysML derivation links requirements to requirements: %s", strings.Join(missing, "; "))
	}
	return nil
}

// designPaths computes the qualified names of the nested design parts.
func (x *sysml) designPaths() {
	var path func(d *req.DesignElement) string
	path = func(d *req.DesignElement) string {
		if p, ok := x.paths[req.Ref(d.ID)]; ok {
			return p
		}
		p := quote(d.ID)
		if pe, ok := x.m.Get(d.Parent); ok {
			p = path(pe.(*req.DesignElement)) + "::" + p
		}
		x.paths[req.Ref(d.ID)] = p
		return p
	}
	for _, d := range req.All[req.DesignElement](x.m) {
		path(d)
	}
}

// ref is how other elements refer to id; ok is false for elements without a
// SysML name (satisfactions).
func (x *sysml) ref(id req.Ref) (string, bool) {
	if p, ok := x.paths[id]; ok {
		return p, true
	}
	e, ok := x.m.Get(id)
	if !ok || req.KindOf(e) == req.KindSatisfaction {
		return "", false
	}
	return quote(string(id)), true
}

// --- vocabulary ---

var relationTags = []string{
	"subgoal", "addresses", "operationalizes", "presumes", "child", "assumes", "alternative",
	"owner", "origin", "proposedBy", "approvedBy", "realization", "threatScenario",
	"contains", "specSubject", "verifiedItem",
}

func (x *sysml) vocabulary() {
	x.line(1, "package Vocabulary {")
	x.line(2, "// Concept schemes")
	for _, s := range x.m.Schemes() {
		x.line(2, "enum def "+kindName(s.ID)+" {")
		if s.Name != "" {
			x.line(3, "doc "+docText(s.Name))
		}
		for _, c := range s.Concepts {
			x.line(3, "enum "+quote(c.ID)+";")
		}
		x.line(2, "}")
	}
	x.blank()
	x.line(2, "// Element tags")
	for _, t := range []string{"Goal", "Hazard", "ThreatScenario", "StandardClause", "SolutionConcept", "Stakeholder", "Specification"} {
		x.line(2, "metadata def "+t+";")
	}
	x.blank()
	x.line(2, "// Relations without a SysML keyword, used as #tag dependency A to B")
	for _, t := range relationTags {
		x.line(2, "metadata def "+t+";")
	}
	x.blank()
	x.line(2, "// Attributes")
	for _, d := range []struct {
		name  string
		attrs [][2]string // name, type
	}{
		{"Lifecycle", [][2]string{{"status", x.kindType("status")}, {"version", "String"}}},
		{"Classification", [][2]string{{"context", x.kindType("requirement-context")}, {"form", x.kindType("form")}, {"concerns", x.kindType("concern") + "[*]"}}},
		{"SafetyInfo", [][2]string{{"asil", x.kindType("asil")}}},
		{"SecurityInfo", [][2]string{{"cal", x.kindType("cal")}}},
		{"GoalInfo", [][2]string{{"pattern", x.kindType("goal-pattern")}, {"soft", "Boolean"}, {"refinement", "String"}}},
		{"ConceptInfo", [][2]string{{"kind", x.kindType("concept-kind")}, {"selected", "Boolean"}}},
		{"AssumptionInfo", [][2]string{{"kind", x.kindType("assumption-kind")}}},
		{"DesignInfo", [][2]string{{"kind", x.kindType("design-kind")}}},
		{"RoleInfo", [][2]string{{"role", x.kindType("role")}}},
		{"SourceInfo", [][2]string{{"source", "String"}}},
		{"ClauseInfo", [][2]string{{"standard", "String"}, {"edition", "String"}, {"clause", "String"}}},
		{"ProvenanceInfo", [][2]string{{"sources", "String[*]"}, {"generatedBy", "String"}, {"approvedOn", "String"}, {"externalTool", "String"}, {"externalID", "String"}}},
		{"ClaimInfo", [][2]string{{"claimID", "String"}, {"claimedBy", "String"}, {"atVersion", "String"}, {"date", "String"}}},
		{"VerificationInfo", [][2]string{{"method", x.kindType("verification-method")}, {"result", x.kindType("verdict")}, {"atVersion", "String"}, {"date", "String"}, {"evidence", "String[*]"}}},
		{"CriterionInfo", [][2]string{{"unit", "String"}, {"condition", "String"}}},
	} {
		x.line(2, "metadata def "+d.name+" {")
		for _, a := range d.attrs {
			x.line(3, "attribute "+a[0]+" : "+a[1]+";")
		}
		x.line(2, "}")
	}
	x.line(1, "}")
	x.blank()
}

// kindName is the enum def name for a scheme: "requirement-context" becomes
// RequirementContextKind.
func kindName(scheme string) string {
	var b strings.Builder
	for _, part := range strings.Split(scheme, "-") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	n := b.String()
	if !strings.HasSuffix(n, "Kind") {
		n += "Kind"
	}
	return n
}

func (x *sysml) kindType(scheme string) string {
	if _, ok := x.m.Scheme(scheme); ok {
		return kindName(scheme)
	}
	return "String"
}

func (x *sysml) conceptValue(scheme string, v req.Ref) string {
	if _, ok := x.m.Scheme(scheme); ok {
		return kindName(scheme) + "::" + quote(string(v))
	}
	return str(string(v))
}

// --- elements ---

// meta is one metadata usage being assembled.
type meta struct {
	name   string
	values [][2]string
}

func (mt *meta) set(attr, value string) {
	if value != "" {
		mt.values = append(mt.values, [2]string{attr, value})
	}
}

func (x *sysml) writeMeta(indent int, mts ...meta) {
	for _, mt := range mts {
		if len(mt.values) == 0 {
			continue
		}
		x.line(indent, "@"+mt.name+" {")
		for _, v := range mt.values {
			x.line(indent+1, v[0]+" = "+v[1]+";")
		}
		x.line(indent, "}")
	}
}

// common writes what every element body has: description, lifecycle,
// rationale and provenance.
func (x *sysml) common(indent int, b *req.Element, docText string) {
	if docText != "" {
		x.line(indent, "doc "+docComment(docText))
	}
	if b.Description != "" && b.Description != docText {
		x.line(indent, "comment "+docComment(b.Description))
	}
	life := meta{name: "Lifecycle"}
	if b.Status != "" {
		life.set("status", x.conceptValue("status", b.Status))
	}
	life.set("version", strOrEmpty(b.Version))
	x.writeMeta(indent, life)
	if r := b.Rationale; r != nil && r.Text != "" {
		x.writeMeta(indent, meta{name: "Rationale", values: [][2]string{{"text", str(r.Text)}}})
	}
	if p := b.Provenance; p != nil {
		pm := meta{name: "ProvenanceInfo"}
		pm.set("sources", strSeq(p.Sources))
		pm.set("generatedBy", strOrEmpty(p.GeneratedBy))
		if !p.ApprovedOn.IsZero() {
			pm.set("approvedOn", str(p.ApprovedOn.UTC().Format(time.RFC3339)))
		}
		pm.set("externalTool", strOrEmpty(p.ExternalTool))
		pm.set("externalID", strOrEmpty(p.ExternalID))
		x.writeMeta(indent, pm)
	}
}

func head(keyword string, b *req.Element) string {
	s := keyword + " <" + quote(b.ID) + ">"
	if b.Name != "" {
		s += " " + quote(b.Name)
	}
	return s
}

func (x *sysml) elements() {
	section := func(title string) { x.line(1, "// "+title) }

	section("Stakeholders")
	for _, s := range req.All[req.Stakeholder](x.m) {
		x.line(1, "#Stakeholder "+head("part def", &s.Element)+" {")
		x.common(2, &s.Element, s.Description)
		if s.Role != "" {
			x.writeMeta(2, meta{"RoleInfo", [][2]string{{"role", x.conceptValue("role", s.Role)}}})
		}
		x.line(1, "}")
	}
	x.blank()

	section("Ideas")
	for _, i := range req.All[req.Idea](x.m) {
		text := i.Statement
		if i.Description != "" {
			text += "\n" + i.Description
		}
		x.line(1, head("comment", &i.Element)+" "+docComment(text))
	}
	x.blank()

	section("Needs")
	for _, n := range req.All[req.Need](x.m) {
		x.line(1, head("concern", &n.Element)+" {")
		x.common(2, &n.Element, n.Statement)
		for i, h := range n.HeldBy {
			x.line(2, fmt.Sprintf("stakeholder holder%d : %s;", i+1, quote(string(h))))
		}
		x.line(1, "}")
	}
	x.blank()

	section("Origins of goals: hazards, threat scenarios, standard clauses")
	for _, e := range x.m.Elems() {
		var tag string
		var mt meta
		switch e := e.(type) {
		case *req.Hazard:
			tag, mt = "Hazard", meta{name: "SourceInfo"}
			mt.set("source", strOrEmpty(e.Source))
		case *req.ThreatScenario:
			tag, mt = "ThreatScenario", meta{name: "SourceInfo"}
			mt.set("source", strOrEmpty(e.Source))
		case *req.StandardClause:
			tag, mt = "StandardClause", meta{name: "ClauseInfo"}
			mt.set("standard", strOrEmpty(e.Standard))
			mt.set("edition", strOrEmpty(e.Edition))
			mt.set("clause", strOrEmpty(e.Clause))
		default:
			continue
		}
		b := req.Base(e)
		x.line(1, "#"+tag+" "+head("item def", b)+" {")
		x.common(2, b, b.Description)
		x.writeMeta(2, mt)
		x.line(1, "}")
	}
	x.blank()

	section("Assumptions")
	for _, a := range req.All[req.Assumption](x.m) {
		x.line(1, head("constraint def", &a.Element)+" {")
		x.common(2, &a.Element, a.Text)
		if a.Kind != "" {
			x.writeMeta(2, meta{"AssumptionInfo", [][2]string{{"kind", x.conceptValue("assumption-kind", a.Kind)}}})
		}
		x.line(1, "}")
	}
	x.blank()

	section("Goals")
	for _, g := range req.All[req.Goal](x.m) {
		x.line(1, "#Goal "+head("requirement", &g.Element)+" {")
		x.common(2, &g.Element, g.Description)
		gm := meta{name: "GoalInfo"}
		if g.Pattern != "" {
			gm.set("pattern", x.conceptValue("goal-pattern", g.Pattern))
		}
		if g.Soft {
			gm.set("soft", "true")
		}
		gm.set("refinement", strOrEmpty(g.Refinement))
		x.writeMeta(2, gm)
		for _, a := range g.Assumes {
			x.line(2, "assume constraint : "+quote(string(a))+";")
		}
		x.line(1, "}")
	}
	x.blank()

	section("Solution concepts")
	for _, c := range req.All[req.SolutionConcept](x.m) {
		x.line(1, "#SolutionConcept "+head("part def", &c.Element)+" {")
		x.common(2, &c.Element, c.Description)
		cm := meta{name: "ConceptInfo"}
		if c.Kind != "" {
			cm.set("kind", x.conceptValue("concept-kind", c.Kind))
		}
		cm.set("selected", strconv.FormatBool(c.Selected))
		x.writeMeta(2, cm)
		x.line(1, "}")
	}
	x.blank()

	section("Requirements")
	for _, r := range req.All[req.Requirement](x.m) {
		x.requirement(r)
	}

	section("Specifications")
	for _, s := range req.All[req.Specification](x.m) {
		x.line(1, "#Specification "+head("requirement def", &s.Element)+" {")
		x.common(2, &s.Element, s.Description)
		x.line(1, "}")
	}
	x.blank()

	section("Design")
	for _, d := range req.All[req.DesignElement](x.m) {
		if d.Parent == "" {
			x.design(1, d)
		}
	}
	x.blank()

	section("Satisfaction claims")
	for _, s := range req.All[req.Satisfaction](x.m) {
		if !req.Active(s) {
			continue
		}
		by, _ := x.ref(s.By)
		x.line(1, "satisfy "+quote(string(s.Requirement))+" by "+by+" {")
		cm := meta{name: "ClaimInfo"}
		cm.set("claimID", str(s.ID))
		cm.set("claimedBy", strOrEmpty(string(s.Owner)))
		cm.set("atVersion", strOrEmpty(s.AtVersion))
		if !s.Date.IsZero() {
			cm.set("date", str(s.Date.UTC().Format(time.RFC3339)))
		}
		x.writeMeta(2, cm)
		x.line(1, "}")
	}
	x.blank()

	section("Verifications")
	for _, v := range req.All[req.Verification](x.m) {
		x.line(1, head("verification", &v.Element)+" {")
		x.common(2, &v.Element, v.Description)
		x.line(2, "objective {")
		for _, r := range v.Verifies {
			x.line(3, "verify "+quote(string(r))+";")
		}
		x.line(2, "}")
		vm := meta{name: "VerificationInfo"}
		if v.Method != "" {
			vm.set("method", x.conceptValue("verification-method", v.Method))
		}
		if v.Result != "" {
			vm.set("result", x.conceptValue("verdict", v.Result))
		}
		vm.set("atVersion", strOrEmpty(v.AtVersion))
		if !v.Date.IsZero() {
			vm.set("date", str(v.Date.UTC().Format(time.RFC3339)))
		}
		vm.set("evidence", strSeq(v.Evidence))
		x.writeMeta(2, vm)
		x.line(1, "}")
	}
	x.blank()
}

func (x *sysml) requirement(r *req.Requirement) {
	x.line(1, head("requirement", &r.Element)+" {")
	x.common(2, &r.Element, r.Text)
	cm := meta{name: "Classification"}
	if r.Context != "" {
		cm.set("context", x.conceptValue("requirement-context", r.Context))
	}
	if r.Form != "" {
		cm.set("form", x.conceptValue("form", r.Form))
	}
	if len(r.Concerns) > 0 {
		var vs []string
		for _, c := range r.Concerns {
			vs = append(vs, x.conceptValue("concern", c))
		}
		cm.set("concerns", seq(vs))
	}
	x.writeMeta(2, cm)
	if r.Safety != nil {
		x.writeMeta(2, meta{"SafetyInfo", [][2]string{{"asil", x.conceptValue("asil", r.Safety.ASIL)}}})
	}
	if r.Security != nil {
		x.writeMeta(2, meta{"SecurityInfo", [][2]string{{"cal", x.conceptValue("cal", r.Security.CAL)}}})
	}
	for _, a := range r.Assumes {
		x.line(2, "assume constraint : "+quote(string(a))+";")
	}
	var declared []req.Ref
	for _, c := range r.Criteria {
		if !slices.Contains(declared, c.Metric) {
			x.line(2, "attribute "+quote(string(c.Metric))+" : Real;")
			declared = append(declared, c.Metric)
		}
	}
	for _, c := range r.Criteria {
		op := c.Operator
		if op == "=" {
			op = "=="
		}
		x.line(2, "require constraint {")
		mt := meta{name: "CriterionInfo"}
		mt.set("unit", strOrEmpty(c.Unit))
		mt.set("condition", strOrEmpty(c.Condition))
		x.writeMeta(3, mt)
		x.line(3, quote(string(c.Metric))+" "+op+" "+strconv.FormatFloat(c.Threshold, 'f', -1, 64))
		x.line(2, "}")
	}
	x.line(1, "}")
}

func (x *sysml) design(indent int, d *req.DesignElement) {
	x.line(indent, head("part", &d.Element)+" {")
	x.common(indent+1, &d.Element, d.Description)
	if d.Kind != "" {
		x.writeMeta(indent+1, meta{"DesignInfo", [][2]string{{"kind", x.conceptValue("design-kind", d.Kind)}}})
	}
	for _, c := range req.All[req.DesignElement](x.m) {
		if c.Parent == req.Ref(d.ID) {
			x.design(indent+1, c)
		}
	}
	x.line(indent, "}")
}

// --- relations ---

func (x *sysml) relations() {
	x.line(1, "// Relations")
	dep := func(tag string, from req.Ref, to ...req.Ref) {
		f, ok := x.ref(from)
		if !ok {
			return
		}
		for _, t := range to {
			if t == "" {
				continue
			}
			if tt, ok := x.ref(t); ok {
				x.line(1, "#"+tag+" dependency "+f+" to "+tt+";")
			}
		}
	}
	for _, e := range x.m.Elems() {
		id := req.Ref(req.Base(e).ID)
		b := req.Base(e)
		if req.KindOf(e) != req.KindSatisfaction {
			dep("owner", id, b.Owner)
		}
		if r := b.Rationale; r != nil {
			dep("assumes", id, r.Assumes...)
			dep("alternative", id, r.Alternatives...)
		}
		if p := b.Provenance; p != nil {
			dep("approvedBy", id, p.ApprovedBy)
		}
		switch e := e.(type) {
		case *req.Idea:
			dep("proposedBy", id, e.ProposedBy)
		case *req.Need:
			dep("origin", id, e.Origin...)
		case *req.Goal:
			dep("subgoal", id, e.Subgoals...)
			dep("addresses", id, e.Addresses...)
		case *req.SolutionConcept:
			dep("addresses", id, e.Addresses...)
		case *req.Requirement:
			dep("operationalizes", id, e.Operationalizes...)
			dep("presumes", id, e.Concept)
			dep("child", id, e.Children...)
			dep("refinement", id, e.Refines...)
			if e.Security != nil {
				dep("threatScenario", id, e.Security.ThreatScenario)
			}
		case *req.Specification:
			dep("specSubject", id, e.Subject)
			dep("contains", id, e.Contains...)
		case *req.DesignElement:
			dep("realization", id, e.Realizes)
		case *req.Verification:
			dep("verifiedItem", id, e.Item)
		}
	}
	for _, d := range x.derived {
		x.line(1, "#derivation connection {")
		x.line(2, "end #original ::> "+quote(string(d[0]))+";")
		x.line(2, "end #derive ::> "+quote(string(d[1]))+";")
		x.line(1, "}")
	}
}

// --- lexical helpers ---

// quote writes an unrestricted name: 'text' with ' and \ escaped.
func quote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// str writes a string literal.
func str(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s) + `"`
}

func strOrEmpty(s string) string {
	if s == "" {
		return ""
	}
	return str(s)
}

func seq(vs []string) string {
	if len(vs) == 1 {
		return vs[0]
	}
	return "(" + strings.Join(vs, ", ") + ")"
}

func strSeq(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	vs := make([]string, len(ss))
	for i, s := range ss {
		vs[i] = str(s)
	}
	return seq(vs)
}

// docComment writes text as a /* */ comment body that can't end early.
func docComment(s string) string {
	return "/* " + strings.ReplaceAll(s, "*/", "* /") + " */"
}

func docText(s string) string { return docComment(s) }
