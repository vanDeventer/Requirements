package req

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

const exampleDir = "../example/ota"

func loadExample(t *testing.T) *Model {
	t.Helper()
	m, err := Load(exampleDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestExampleIsValid(t *testing.T) {
	m := loadExample(t)
	if is := m.Validate(); len(is) > 0 {
		t.Errorf("Validate: %v", is)
	}
	want := []Issue{{ID: "REQ-005", Field: "criteria", Message: "no acceptance criterion, so it can't be verified"}}
	if got := m.Gaps(); !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %v, want %v", got, want)
	}
}

func TestExampleEvidence(t *testing.T) {
	m := loadExample(t)
	for _, c := range []struct {
		id                  Ref
		satisfied, verified bool
	}{
		{"REQ-001", true, true},   // claim verified at the claimed version
		{"REQ-002", true, false},  // latest verification failed
		{"REQ-003", true, false},  // satisfied only through its refinement REQ-004
		{"REQ-004", true, false},  // claim without verification
		{"REQ-005", false, false}, // no claim
	} {
		e, err := m.Evidence(c.id)
		if err != nil {
			t.Fatal(err)
		}
		if e.Satisfied != c.satisfied || e.Verified != c.verified {
			t.Errorf("%s: satisfied %v verified %v, want %v %v (%v)", c.id, e.Satisfied, e.Verified, c.satisfied, c.verified, e.Reasons)
		}
	}
	if _, err := m.Evidence("G-001"); err == nil {
		t.Error("Evidence of a goal: want an error")
	}
}

func TestExampleUnverified(t *testing.T) {
	m := loadExample(t)
	ids := func(es []Evidence) []Ref {
		var r []Ref
		for _, e := range es {
			r = append(r, e.Requirement)
		}
		return r
	}
	all, err := m.Unverified("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(all), []Ref{"REQ-002", "REQ-003", "REQ-004", "REQ-005"}; !slices.Equal(got, want) {
		t.Errorf("Unverified() = %v, want %v", got, want)
	}
	spec, err := m.Unverified("SPEC-001")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(spec), []Ref{"REQ-004", "REQ-005"}; !slices.Equal(got, want) {
		t.Errorf("Unverified(SPEC-001) = %v, want %v", got, want)
	}
	if _, err := m.Unverified("REQ-001"); err == nil {
		t.Error("Unverified of a requirement: want an error")
	}
}

func TestExampleCoverage(t *testing.T) {
	m := loadExample(t)
	for _, c := range []struct {
		origin  Ref
		covered bool
		reqs    []Ref
	}{
		{"HAZ-001", true, []Ref{"REQ-001"}},
		{"CL-R156-7-2", false, []Ref{"REQ-002"}},
		{"TS-001", false, []Ref{"REQ-002", "REQ-005"}}, // REQ-005 names TS-001 in its security attributes
		{"NEED-001", false, []Ref{"REQ-001", "REQ-002", "REQ-003", "REQ-004", "REQ-006"}},
		{"HAZ-002", true, []Ref{"REQ-006"}},
	} {
		r, err := m.Coverage(c.origin)
		if err != nil {
			t.Fatal(err)
		}
		var got []Ref
		for _, e := range r.Requirements {
			got = append(got, e.Requirement)
		}
		if r.Covered != c.covered || !slices.Equal(got, c.reqs) {
			t.Errorf("%s: covered %v %v, want %v %v (%v)", c.origin, r.Covered, got, c.covered, c.reqs, r.Reasons)
		}
	}
	if _, err := m.Coverage("REQ-001"); err == nil {
		t.Error("Coverage of a requirement: want an error")
	}
}

func TestExampleImpact(t *testing.T) {
	m := loadExample(t)
	r, err := m.Impact("AS-001")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Direct, []Ref{"REQ-001"}) || !slices.Equal(r.Affected, []Ref{"DE-002", "REQ-005", "VER-001"}) {
		t.Errorf("Impact(AS-001) = %+v", r)
	}
	r, err = m.Impact("AS-002")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Direct, []Ref{"G-001"}) || !slices.Contains(r.Affected, "REQ-002") {
		t.Errorf("Impact(AS-002) = %+v", r)
	}
	// A clause with a new edition: what does it touch?
	r, err = m.Impact("CL-R156-7-2")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Direct, []Ref{"G-003"}) || !slices.Equal(r.Affected, []Ref{"DE-003", "REQ-002", "VER-002"}) {
		t.Errorf("Impact(CL-R156-7-2) = %+v", r)
	}
	if _, err := m.Impact("NOPE"); err == nil {
		t.Error("Impact of a missing element: want an error")
	}
}

func TestGapsContextAndApproval(t *testing.T) {
	gen := &Requirement{Element: Element{ID: "R-gen", Status: "approved", Provenance: &Provenance{GeneratedBy: "some-model 1"}},
		Text: "x", Context: "product", Refines: []Ref{"R-prod"}, Criteria: []AcceptanceCriterion{{Metric: "m", Operator: "="}}}
	m := small(
		&Goal{Element: el("G")},
		&Requirement{Element: el("R-prod"), Text: "x", Context: "product", Operationalizes: []Ref{"G"}, Children: []Ref{"R-tool"}},
		&Requirement{Element: el("R-tool"), Text: "x", Context: "tool", Criteria: []AcceptanceCriterion{{Metric: "m", Operator: "="}}},
		gen,
	)
	got := map[string]bool{}
	for _, i := range m.Gaps() {
		got[string(i.ID)+"."+i.Field] = true
	}
	if !got["R-prod.children"] {
		t.Errorf("Gaps missed the product parent with a tool child: %v", got)
	}
	if !got["R-gen.provenance.approvedBy"] {
		t.Errorf("Gaps missed the approved AI-generated requirement without approver: %v", got)
	}
	if got["R-gen.refines"] {
		t.Errorf("Gaps flagged a refinement within one context: %v", got)
	}
	gen.Provenance.ApprovedBy = "SH-x"
	for _, i := range m.Gaps() {
		if i.ID == "R-gen" && i.Field == "provenance.approvedBy" {
			t.Error("Gaps still flags R-gen after an approver is recorded")
		}
	}
}

func TestExampleTrace(t *testing.T) {
	m := loadExample(t)
	tr, err := m.Trace("REQ-001")
	if err != nil {
		t.Fatal(err)
	}
	has := func(es []Edge, from Ref, rel string, to Ref) bool {
		return slices.ContainsFunc(es, func(e Edge) bool { return e.From == from && e.Rel == rel && e.To == to })
	}
	for _, want := range []Edge{
		{From: "REQ-001", Rel: "operationalizes", To: "G-002"},
		{From: "G-002", Rel: "addresses", To: "HAZ-001"},
		{From: "G-001", Rel: "subgoals", To: "G-002"},
		{From: "NEED-001", Rel: "origin", To: "IDEA-001"},
	} {
		if !has(tr.Up, want.From, want.Rel, want.To) {
			t.Errorf("Trace(REQ-001).Up lacks %s --%s--> %s", want.From, want.Rel, want.To)
		}
	}
	for _, want := range []Edge{
		{From: "DE-002", Rel: "satisfies", To: "REQ-001"},
		{From: "VER-001", Rel: "verifies", To: "REQ-001"},
		{From: "REQ-005", Rel: "derivedFrom", To: "DE-002"},
	} {
		if !has(tr.Down, want.From, want.Rel, want.To) {
			t.Errorf("Trace(REQ-001).Down lacks %s --%s--> %s", want.From, want.Rel, want.To)
		}
	}
	if has(tr.Up, "REQ-001", "assumes", "AS-001") {
		t.Error("Trace followed a side relation")
	}
}

func TestFind(t *testing.T) {
	m := loadExample(t)
	got := m.Find("PARKING brake")
	if len(got) != 1 || got[0].ID != "REQ-001" {
		t.Errorf("Find = %v", got)
	}
	if got := m.Find("  "); got != nil {
		t.Errorf("Find(blank) = %v", got)
	}
}

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"REQ-042": true, "a": true, "CL-R156-7-2": true,
		"": false, "1REQ": false, "-x": false, "REQ_1": false, "REQ 1": false, "Ré": false, "a/b": false,
	} {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v", id, got)
		}
	}
}

// small builds a model with the schemes the tests below use.
func small(es ...Elem) *Model {
	m := NewModel()
	m.PutScheme(&Scheme{ID: "status", Concepts: []Concept{{ID: "approved"}, {ID: "rejected"}}})
	m.PutScheme(&Scheme{ID: "concern", Concepts: []Concept{{ID: "safety"}, {ID: "security"}}})
	m.PutScheme(&Scheme{ID: "requirement-context", Concepts: []Concept{{ID: "product"}, {ID: "tool"}, {ID: "training-data"}}})
	m.PutScheme(&Scheme{ID: "verdict", Concepts: []Concept{{ID: "pass"}, {ID: "fail"}}})
	m.PutScheme(&Scheme{ID: "verification-method", Concepts: []Concept{{ID: "test"}}})
	for _, e := range es {
		m.Put(e)
	}
	return m
}

func el(id string) Element { return Element{ID: id} }

func TestValidateFindsProblems(t *testing.T) {
	m := small(
		&Goal{Element: el("G-1"), Subgoals: []Ref{"G-2"}},                                              // no refinement
		&Goal{Element: el("G-2"), Refinement: "AND", Subgoals: []Ref{"G-1"}},                           // cycle G-1 → G-2 → G-1
		&Requirement{Element: el("R-1"), Text: "x", Context: "product", Operationalizes: []Ref{"R-2"}}, // wrong kind
		&Requirement{Element: el("R-2"), Text: " ", Children: []Ref{"R-9"}},                            // blank text, dangling
		&Requirement{Element: Element{ID: "R-3", Status: "done"}, Text: "x", Context: "product", Form: "functional"},
		&Requirement{Element: el("R-4"), Text: "x", Context: "product", Safety: &SafetyAttrs{ASIL: "B"},
			Criteria: []AcceptanceCriterion{{Metric: "m", Operator: "=="}}},
		&Requirement{Element: el("R_5"), Text: "x", Context: "product", Refines: []Ref{"R_5"}},
	)
	got := map[string]bool{}
	for _, i := range m.Validate() {
		got[string(i.ID)+"."+i.Field] = true
	}
	for _, want := range []string{
		"G-1.refinement",
		"G-1.subgoals", // the cycle
		"R-1.operationalizes",
		"R-2.text",
		"R-2.context", // missing
		"R-2.children",
		"R-3.status",            // not in scheme
		"R-3.form",              // scheme not defined
		"R-4.safety",            // without the safety concern
		"R-4.criteria.operator", // "=="
		"R-4.criteria.metric",   // scheme not defined
		"R_5.id",
		"R_5.refines", // refers to itself
	} {
		if !got[want] {
			t.Errorf("Validate missed %s; got %v", want, got)
		}
	}
}

func day(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }

func TestEvidenceRules(t *testing.T) {
	sat := func(id, req, by, ver string) *Satisfaction {
		return &Satisfaction{Element: el(id), Requirement: Ref(req), By: Ref(by), AtVersion: ver, Date: day(1)}
	}
	ver := func(id, req, item, at, result string, d int) *Verification {
		return &Verification{Element: el(id), Verifies: []Ref{Ref(req)}, Item: Ref(item), Method: "test", Result: Ref(result), AtVersion: at, Date: day(d)}
	}
	rejected := ver("V-rej", "R-ok", "D", "2", "fail", 9)
	rejected.Status = "rejected"
	m := small(
		&DesignElement{Element: el("D")},
		&DesignElement{Element: el("E")},
		// Verified at an older version than claimed.
		&Requirement{Element: el("R-old"), Text: "x", Context: "product"}, sat("S-old", "R-old", "D", "2"), ver("V-old", "R-old", "D", "1", "pass", 2),
		// Passed, then failed later.
		&Requirement{Element: el("R-regr"), Text: "x", Context: "product"}, sat("S-regr", "R-regr", "D", "2"),
		ver("V-r1", "R-regr", "D", "2", "pass", 2), ver("V-r2", "R-regr", "D", "2", "fail", 3),
		// A later rejected failure doesn't count.
		&Requirement{Element: el("R-ok"), Text: "x", Context: "product"}, sat("S-ok", "R-ok", "D", "2"), ver("V-ok", "R-ok", "D", "2", "pass", 2), rejected,
		// Two claims; only one verified.
		&Requirement{Element: el("R-two"), Text: "x", Context: "product"}, sat("S-t1", "R-two", "D", "2"), sat("S-t2", "R-two", "E", "1"),
		ver("V-t1", "R-two", "D", "2", "pass", 2),
		// Verification of a different design element than claimed.
		&Requirement{Element: el("R-other"), Text: "x", Context: "product"}, sat("S-oth", "R-other", "D", "2"), ver("V-oth", "R-other", "E", "2", "pass", 2),
		// Decomposition: all children needed; a rejected child doesn't count.
		&Requirement{Element: el("P-all"), Text: "x", Context: "product", Children: []Ref{"R-ok", "R-gone"}},
		&Requirement{Element: el("P-some"), Text: "x", Context: "product", Children: []Ref{"R-ok", "R-old"}},
		&Requirement{Element: Element{ID: "R-gone", Status: "rejected"}, Text: "x", Context: "product"},
	)
	if is := m.Validate(); len(is) > 0 {
		t.Fatalf("Validate: %v", is)
	}
	for _, c := range []struct {
		id                  Ref
		satisfied, verified bool
	}{
		{"R-old", true, false},
		{"R-regr", true, false},
		{"R-ok", true, true},
		{"R-two", true, false},
		{"R-other", true, false},
		{"P-all", true, true},
		{"P-some", true, false},
	} {
		e, err := m.Evidence(c.id)
		if err != nil {
			t.Fatal(err)
		}
		if e.Satisfied != c.satisfied || e.Verified != c.verified {
			t.Errorf("%s: satisfied %v verified %v, want %v %v (%v)", c.id, e.Satisfied, e.Verified, c.satisfied, c.verified, e.Reasons)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	src := loadExample(t)
	dir := t.TempDir()
	for _, id := range []string{"status", "role", "goal-pattern", "concept-kind", "form", "concern", "metric",
		"assumption-kind", "requirement-context", "asil", "cal", "design-kind", "verification-method", "verdict"} {
		s, ok := src.Scheme(id)
		if !ok {
			t.Fatalf("example lacks scheme %s", id)
		}
		if err := SaveScheme(dir, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range src.Elems() {
		if err := Save(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, src) {
		t.Error("model differs after Save and Load")
	}
	fi, err := os.Stat(filepath.Join(dir, "requirements", "REQ-001.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("file mode %v, want 0644", fi.Mode().Perm())
	}
	tmps, _ := filepath.Glob(filepath.Join(dir, "*", ".tmp-*"))
	if len(tmps) > 0 {
		t.Errorf("temporary files left: %v", tmps)
	}

	if err := Delete(dir, KindRequirement, "REQ-005"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Load(dir); got.has("REQ-005") {
		t.Error("REQ-005 still loads after Delete")
	}
	if err := Save(dir, &Requirement{Element: el("../x"), Text: "x", Context: "product"}); err == nil {
		t.Error("Save with a path in the id: want an error")
	}
}

func TestLoadReportsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("requirements/R-1.json", `{"id": "R-1", "text": "x", "txet": "typo"}`)
	write("requirements/R-2.json", `{"id": "R-3", "text": "x"}`)
	write("goals/R-4.json", `{"id": "R-4"}`)
	write("needs/R-4.json", `{"id": "R-4", "statement": "x"}`)
	write("requirements/notes.txt", `ignored`)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`unknown field "txet"`, `id "R-3" does not match file name`, `id "R-4" already used`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}
