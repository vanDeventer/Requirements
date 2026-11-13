package export

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/vanDeventer/Requirements/req"
)

func example(t *testing.T) *req.Model {
	t.Helper()
	m, err := req.Load("../example/ota")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// --- a reader for the Turtle subset the exporter writes ---

type triple struct{ s, p, o string }

type ttlReader struct {
	src     string
	pos     int
	prefix  map[string]string
	triples []triple
	blanks  int
}

func (r *ttlReader) fail(format string, args ...any) {
	line := strings.Count(r.src[:r.pos], "\n") + 1
	panic(fmt.Sprintf("line %d: %s", line, fmt.Sprintf(format, args...)))
}

func (r *ttlReader) skip() {
	for r.pos < len(r.src) {
		switch c := r.src[r.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			r.pos++
		case c == '#':
			for r.pos < len(r.src) && r.src[r.pos] != '\n' {
				r.pos++
			}
		default:
			return
		}
	}
}

func (r *ttlReader) peek() byte {
	r.skip()
	if r.pos >= len(r.src) {
		return 0
	}
	return r.src[r.pos]
}

func (r *ttlReader) expect(c byte) {
	if r.peek() != c {
		r.fail("want %q, got %q", c, r.peek())
	}
	r.pos++
}

var pname = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*)?:([A-Za-z0-9][A-Za-z0-9_-]*)?`)

// term reads an IRI, prefixed name, literal, keyword or blank node.
func (r *ttlReader) term() string {
	switch c := r.peek(); {
	case c == '<':
		end := strings.IndexByte(r.src[r.pos:], '>')
		if end < 0 {
			r.fail("unterminated IRI")
		}
		iri := r.src[r.pos+1 : r.pos+end]
		if strings.ContainsAny(iri, " \"{}|^`\\") {
			r.fail("bad character in IRI %q", iri)
		}
		r.pos += end + 1
		return iri
	case c == '"':
		r.pos++
		var b strings.Builder
		for {
			if r.pos >= len(r.src) || r.src[r.pos] == '\n' {
				r.fail("unterminated string")
			}
			ch := r.src[r.pos]
			r.pos++
			if ch == '"' {
				break
			}
			if ch == '\\' {
				esc := r.src[r.pos]
				r.pos++
				switch esc {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case '"', '\\':
					b.WriteByte(esc)
				default:
					r.fail("unknown escape \\%c", esc)
				}
				continue
			}
			b.WriteByte(ch)
		}
		typ := ""
		if strings.HasPrefix(r.src[r.pos:], "^^") {
			r.pos += 2
			typ = "^^" + r.term()
		}
		return `"` + b.String() + `"` + typ
	case c == '(':
		r.pos++
		var items []string
		for r.peek() != ')' {
			items = append(items, r.term())
		}
		r.pos++
		head := rdfNS + "nil"
		for i := len(items) - 1; i >= 0; i-- {
			r.blanks++
			node := fmt.Sprintf("_:b%d", r.blanks)
			r.triples = append(r.triples, triple{node, rdfNS + "first", items[i]}, triple{node, rdfNS + "rest", head})
			head = node
		}
		return head
	case c >= '0' && c <= '9':
		start := r.pos
		for r.pos < len(r.src) && r.src[r.pos] >= '0' && r.src[r.pos] <= '9' {
			r.pos++
		}
		return `"` + r.src[start:r.pos] + `"^^http://www.w3.org/2001/XMLSchema#integer`
	case c == '[':
		r.pos++
		r.blanks++
		node := fmt.Sprintf("_:b%d", r.blanks)
		if r.peek() != ']' {
			r.predicates(node)
		}
		r.expect(']')
		return node
	}
	for _, kw := range []string{"true", "false", "a"} {
		rest := r.src[r.pos:]
		if strings.HasPrefix(rest, kw) && (len(rest) == len(kw) || strings.ContainsRune(" \t\n;,.]", rune(rest[len(kw)]))) {
			r.pos += len(kw)
			if kw == "a" {
				return "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
			}
			return kw
		}
	}
	m := pname.FindStringSubmatch(r.src[r.pos:])
	if m == nil {
		r.fail("unexpected %q", r.src[r.pos:min(r.pos+20, len(r.src))])
	}
	ns, ok := r.prefix[m[1]]
	if !ok {
		r.fail("undeclared prefix %q", m[1])
	}
	r.pos += len(m[0])
	return ns + m[2]
}

func (r *ttlReader) predicates(s string) {
	for {
		p := r.term()
		for {
			r.triples = append(r.triples, triple{s, p, r.term()})
			if r.peek() != ',' {
				break
			}
			r.pos++
		}
		if r.peek() != ';' {
			return
		}
		r.pos++
		if c := r.peek(); c == '.' || c == ']' {
			return
		}
	}
}

func readTurtle(src string) (ts []triple, err error) {
	r := &ttlReader{src: src, prefix: map[string]string{}}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()
	for r.peek() != 0 {
		if strings.HasPrefix(r.src[r.pos:], "@prefix") {
			r.pos += len("@prefix")
			r.skip()
			m := regexp.MustCompile(`^([A-Za-z][A-Za-z0-9_-]*)?:`).FindStringSubmatch(r.src[r.pos:])
			if m == nil {
				r.fail("bad prefix")
			}
			r.pos += len(m[0])
			r.prefix[m[1]] = r.term()
			r.expect('.')
			continue
		}
		r.predicates(r.term())
		r.expect('.')
	}
	return r.triples, nil
}

// --- Turtle ---

const (
	rdfNS = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	base  = "https://example.org/requirements/"
	vocab = DefaultVocab
	rdfT  = "http://www.w3.org/1999/02/22-rdf-syntax-ns#type"
)

func hasTriple(ts []triple, s, p, o string) bool {
	return slices.Contains(ts, triple{s, p, o})
}

func objects(ts []triple, s, p string) []string {
	var out []string
	for _, t := range ts {
		if t.s == s && t.p == p {
			out = append(out, t.o)
		}
	}
	return out
}

func TestTurtleExample(t *testing.T) {
	m := example(t)
	var buf bytes.Buffer
	if err := Turtle(&buf, m, TurtleOptions{}); err != nil {
		t.Fatal(err)
	}
	ts, err := readTurtle(buf.String())
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	for _, e := range m.Elems() {
		id := req.Base(e).ID
		if !hasTriple(ts, base+id, rdfT, vocab+string(req.KindOf(e))) {
			t.Errorf("%s has no type triple", id)
		}
	}
	for _, w := range []triple{
		{base + "REQ-001", "http://open-services.net/ns/rm#satisfiedBy", base + "DE-002"},
		{base + "REQ-001", vocab + "operationalizes", base + "G-002"},
		{base + "REQ-001", vocab + "asil", base + "scheme/asil/B"},
		{base + "REQ-004", vocab + "refines", base + "REQ-003"},
		{base + "G-001", vocab + "subgoal", base + "G-005"},
		{base + "SAT-001", "http://purl.org/dc/terms/date", `"2026-10-02T09:00:00Z"^^http://www.w3.org/2001/XMLSchema#dateTime`},
		{base + "REQ-007", "http://www.w3.org/ns/prov#wasAttributedTo", base + "SH-safety"},
		{base + "REQ-007", "http://www.w3.org/ns/prov#wasDerivedFrom", "https://example.org/docs/perception-data-spec.pdf"},
		{base + "scheme/verification-method/sil-test", "http://www.w3.org/2004/02/skos/core#broader", base + "scheme/verification-method/test"},
		{base + "SC-002", vocab + "selected", "false"},
	} {
		if !hasTriple(ts, w.s, w.p, w.o) {
			t.Errorf("missing %v", w)
		}
	}
	// The criterion is a blank node with a typed threshold.
	crit := objects(ts, base+"REQ-004", vocab+"criterion")
	if len(crit) != 1 || !hasTriple(ts, crit[0], vocab+"threshold", `"20"^^http://www.w3.org/2001/XMLSchema#decimal`) {
		t.Errorf("REQ-004 criterion: %v", crit)
	}
	// A rejected claim gets no direct satisfiedBy link.
	if strings.Count(buf.String(), "oslc_rm:satisfiedBy") != len(req.All[req.Satisfaction](m)) {
		t.Error("expected one satisfiedBy per active satisfaction")
	}
}

func TestTurtleEscaping(t *testing.T) {
	m := example(t)
	r, _ := m.Get("REQ-003")
	tricky := "Quote \" backslash \\ tab \t newline\nend"
	r.(*req.Requirement).Text = tricky
	v, _ := m.Get("VER-001")
	v.(*req.Verification).Evidence = []string{"report 4711 on the shared drive", "https://example.org/x"}
	var buf bytes.Buffer
	if err := Turtle(&buf, m, TurtleOptions{Base: "urn:x-test:"}); err != nil {
		t.Fatal(err)
	}
	ts, err := readTurtle(buf.String())
	if err != nil {
		t.Fatal(err)
	}
	if got := objects(ts, "urn:x-test:REQ-003", vocab+"text"); len(got) != 1 || got[0] != `"`+tricky+`"` {
		t.Errorf("text = %q", got)
	}
	ev := objects(ts, "urn:x-test:VER-001", vocab+"evidence")
	if !slices.Contains(ev, `"report 4711 on the shared drive"`) || !slices.Contains(ev, "https://example.org/x") {
		t.Errorf("evidence = %v", ev)
	}
	if err := Turtle(&buf, m, TurtleOptions{Base: "not an iri"}); err == nil {
		t.Error("relative base: want an error")
	}
}

func TestInvalidModelRefused(t *testing.T) {
	m := example(t)
	r, _ := m.Get("REQ-001")
	r.(*req.Requirement).Operationalizes = []req.Ref{"G-404"}
	var ie *InvalidError
	if err := Turtle(&bytes.Buffer{}, m, TurtleOptions{}); !errors.As(err, &ie) || len(ie.Issues) != 1 {
		t.Errorf("Turtle: %v", err)
	}
	if err := SysML(&bytes.Buffer{}, m, SysMLOptions{}); !errors.As(err, &ie) {
		t.Errorf("SysML: %v", err)
	}
}

// --- SysML ---

// sysmlCheck lexes the output: comments, strings and quoted names must be
// terminated, braces balanced, and every quoted name used in a relation,
// satisfy, verify, assume or derivation must be a declared short name.
func sysmlCheck(t *testing.T, src string) {
	t.Helper()
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`<'((?:[^'\\]|\\.)*)'>`).FindAllStringSubmatch(src, -1) {
		declared[m[1]] = true
	}
	depth := 0
	var code strings.Builder // src without comments and strings
	for i := 0; i < len(src); i++ {
		switch {
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				t.Fatalf("unterminated comment at %d", i)
			}
			i += end + 3
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				i++
			}
			code.WriteByte('\n')
		case src[i] == '"':
			j := i + 1
			for ; j < len(src) && src[j] != '"'; j++ {
				if src[j] == '\\' {
					j++
				}
				if src[j] == '\n' {
					t.Fatalf("newline in string at %d", i)
				}
			}
			i = j
			code.WriteString(`""`)
		case src[i] == '{':
			depth++
			code.WriteByte('{')
		case src[i] == '}':
			depth--
			if depth < 0 {
				t.Fatalf("unbalanced } at %d", i)
			}
			code.WriteByte('}')
		default:
			code.WriteByte(src[i])
		}
	}
	if depth != 0 {
		t.Fatalf("unbalanced braces: depth %d at end", depth)
	}
	use := regexp.MustCompile(`^\s*(#\w+ dependency|satisfy|verify|assume constraint :|end #\w+ ::>)`)
	name := regexp.MustCompile(`'((?:[^'\\]|\\.)*)'`)
	for _, l := range strings.Split(code.String(), "\n") {
		if !use.MatchString(l) {
			continue
		}
		for _, m := range name.FindAllStringSubmatch(l, -1) {
			if !declared[m[1]] {
				t.Errorf("reference to undeclared %q in: %s", m[1], strings.TrimSpace(l))
			}
		}
	}
}

func TestSysMLExample(t *testing.T) {
	m := example(t)
	var buf bytes.Buffer
	if err := SysML(&buf, m, SysMLOptions{Package: "OTA example"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	sysmlCheck(t, out)
	for _, w := range []string{
		"package 'OTA example' {",
		"enum def RequirementContextKind {",
		"enum 'training-data';",
		"requirement <'REQ-001'> 'Install only when parked' {",
		"#Goal requirement <'G-001'> 'Software is updated over the air' {",
		"concern <'NEED-001'> 'Fixes without a workshop visit' {",
		"stakeholder holder1 : 'SH-driver';",
		"assume constraint : 'AS-001';",
		"attribute 'install-time-p95' : Real;",
		"'install-time-p95' <= 20",
		"'installs-started-while-moving' == 0",
		"satisfy 'REQ-001' by 'DE-001'::'DE-002' {",
		"satisfy 'REQ-002' by 'DE-001'::'DE-002'::'DE-003' {",
		"verify 'REQ-006';",
		"result = VerdictKind::'pass';",
		"#refinement dependency 'REQ-004' to 'REQ-003';",
		"#operationalizes dependency 'REQ-001' to 'G-002';",
		"end #original ::> 'REQ-006';",
		"end #derive ::> 'REQ-007';",
		"concerns = ConcernKind::'safety';",
		"generatedBy = \"example-local-model 1.0\";",
		"#realization dependency 'DE-001'::'DE-002' to 'SC-001';",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q", w)
		}
	}
}

func TestSysMLEscapingAndClaims(t *testing.T) {
	m := example(t)
	r, _ := m.Get("REQ-003")
	r.(*req.Requirement).Name = `Driver's "quick" install`
	r.(*req.Requirement).Text = "Ends */ early? \\ no"
	s, _ := m.Get("SAT-002")
	s.(*req.Satisfaction).Status = "rejected"
	var buf bytes.Buffer
	if err := SysML(&buf, m, SysMLOptions{}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	sysmlCheck(t, out)
	if !strings.Contains(out, `requirement <'REQ-003'> 'Driver\'s "quick" install' {`) {
		t.Error("name not escaped")
	}
	if !strings.Contains(out, "doc /* Ends * / early? \\ no */") {
		t.Error("doc comment not protected")
	}
	if strings.Contains(out, "satisfy 'REQ-002'") {
		t.Error("rejected claim exported as satisfy")
	}
}

func TestSysMLDerivationNeedsSatisfaction(t *testing.T) {
	m := example(t)
	r, _ := m.Get("REQ-005")
	r.(*req.Requirement).DerivedFrom = []req.Ref{"DE-001"} // the vehicle satisfies nothing
	err := SysML(&bytes.Buffer{}, m, SysMLOptions{})
	if err == nil || !strings.Contains(err.Error(), "REQ-005 is derived from DE-001, which satisfies no requirement") {
		t.Errorf("err = %v", err)
	}
}

// --- ontology and shapes ---

const (
	owlNS  = "http://www.w3.org/2002/07/owl#"
	rdfsNS = "http://www.w3.org/2000/01/rdf-schema#"
	shNS   = "http://www.w3.org/ns/shacl#"
)

func readOntology(t *testing.T) []triple {
	t.Helper()
	ts, err := readTurtle(string(Ontology()))
	if err != nil {
		t.Fatalf("ontology: %v", err)
	}
	return ts
}

// declared maps each req: term the ontology declares to its OWL type.
func declared(ts []triple) map[string]string {
	d := map[string]string{}
	for _, tr := range ts {
		if tr.p == rdfT && strings.HasPrefix(tr.s, vocab) && strings.HasPrefix(tr.o, owlNS) {
			d[tr.s] = strings.TrimPrefix(tr.o, owlNS)
		}
	}
	return d
}

func TestOntologyCoversExport(t *testing.T) {
	onto := readOntology(t)
	decl := declared(onto)
	for term, typ := range decl {
		if typ == "TransitiveProperty" {
			continue
		}
		if len(objects(onto, term, rdfsNS+"label")) != 1 || len(objects(onto, term, rdfsNS+"comment")) != 1 {
			t.Errorf("%s lacks a label or comment", term)
		}
	}
	for _, k := range req.Kinds() {
		if decl[vocab+string(k)] != "Class" {
			t.Errorf("kind %s is not declared as a class", k)
		}
	}

	m := example(t)
	var buf bytes.Buffer
	if err := Turtle(&buf, m, TurtleOptions{Computed: true}); err != nil {
		t.Fatal(err)
	}
	data, err := readTurtle(buf.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range data {
		for _, term := range []string{tr.p, tr.o} {
			if term == tr.o && tr.p != rdfT {
				continue // only classes in object position
			}
			if strings.HasPrefix(term, vocab) && decl[term] == "" {
				t.Errorf("the export uses %s, which the ontology doesn't declare", term)
			}
		}
	}

	// Links between elements carry no domain or range: a reasoner would turn
	// a wrong-kind link into a false type and hide it from SHACL.
	for _, tr := range onto {
		if (tr.p == rdfsNS+"domain" || tr.p == rdfsNS+"range") && strings.HasPrefix(tr.o, vocab) {
			t.Errorf("%s has %s %s", tr.s, tr.p, tr.o)
		}
	}
	// Mixed IRI-or-text properties must be annotation properties.
	for _, p := range []string{"evidence", "source"} {
		if decl[vocab+p] != "AnnotationProperty" {
			t.Errorf("req:%s is %s, want AnnotationProperty", p, decl[vocab+p])
		}
	}
}

func TestComputedAndProvenance(t *testing.T) {
	m := example(t)
	r, _ := m.Get("REQ-007")
	req.Base(r).Provenance.Sources = append(req.Base(r).Provenance.Sources, "Perception data spec, rev. C")
	var buf bytes.Buffer
	if err := Turtle(&buf, m, TurtleOptions{Computed: true}); err != nil {
		t.Fatal(err)
	}
	ts, err := readTurtle(buf.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []triple{
		{base + "REQ-001", vocab + "isVerified", "true"},
		{base + "REQ-002", vocab + "isSatisfied", "true"},
		{base + "REQ-002", vocab + "isVerified", "false"},
		{base + "HAZ-001", vocab + "isCovered", "true"},
		{base + "CL-R156-7-2", vocab + "isCovered", "false"},
		{base + "SC-001", vocab + "conceptFor", base + "G-001"},
		{base + "REQ-007", vocab + "sourceNote", `"Perception data spec, rev. C"`},
	} {
		if !hasTriple(ts, w.s, w.p, w.o) {
			t.Errorf("missing %v", w)
		}
	}
	if objects(ts, base+"SC-002", vocab+"isVerified") != nil {
		t.Error("computed verdict on a non-requirement")
	}
	buf.Reset()
	if err := Turtle(&buf, m, TurtleOptions{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "req:isVerified") {
		t.Error("computed triples without Computed")
	}
}

func TestShapes(t *testing.T) {
	m := example(t)
	decl := declared(readOntology(t))
	for _, gaps := range []bool{false, true} {
		var buf bytes.Buffer
		if err := Shapes(&buf, m, ShapesOptions{Structure: !gaps, Gaps: gaps}); err != nil {
			t.Fatal(err)
		}
		ts, err := readTurtle(buf.String())
		if err != nil {
			t.Fatalf("gaps=%v: %v", gaps, err)
		}
		warnings := 0
		for _, tr := range ts {
			switch tr.p {
			case shNS + "path", shNS + "inversePath", shNS + "targetClass", shNS + "class", shNS + "targetObjectsOf":
				if strings.HasPrefix(tr.o, vocab) && decl[tr.o] == "" {
					t.Errorf("shapes use %s, which the ontology doesn't declare", tr.o)
				}
			case shNS + "severity":
				if tr.o == shNS+"Warning" {
					warnings++
				}
			}
		}
		if gaps != (warnings > 0) {
			t.Errorf("gaps=%v: %d warning shapes", gaps, warnings)
		}
		out := buf.String()
		if gaps != !strings.Contains(out, "sh:Violation") {
			t.Errorf("gaps=%v: structure and gap shapes mixed", gaps)
		}
		// Allowed concept values come from the model's schemes.
		if !gaps && !strings.Contains(out, "sh:path req:context ; sh:minCount 1 ; sh:maxCount 1 ; sh:in ( <"+base+"scheme/requirement-context/product> <"+base+"scheme/requirement-context/tool> <"+base+"scheme/requirement-context/training-data> )") {
			t.Error("context shape lacks the scheme's values")
		}
		if gaps && !strings.Contains(out, "sh:path req:status ; sh:minCount 1 ; sh:in (") {
			t.Error("the inactive exemption must require a status, or elements without one are exempt")
		}
	}
	if err := Shapes(&bytes.Buffer{}, m, ShapesOptions{Structure: true, Base: "relative/"}); err == nil {
		t.Error("relative base: want an error")
	}
	if err := Shapes(&bytes.Buffer{}, m, ShapesOptions{}); err == nil {
		t.Error("no shape set chosen: want an error")
	}
}
