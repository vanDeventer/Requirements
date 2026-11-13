package exchange

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vanDeventer/Requirements/req"
)

const sampleReqIF = `<?xml version="1.0" encoding="UTF-8"?>
<REQ-IF xmlns="http://www.omg.org/spec/ReqIF/20110401/reqif.xsd" xmlns:xhtml="http://www.w3.org/1999/xhtml">
  <THE-HEADER><REQ-IF-HEADER IDENTIFIER="h"><TITLE>Update manager</TITLE></REQ-IF-HEADER></THE-HEADER>
  <CORE-CONTENT><REQ-IF-CONTENT>
    <SPEC-TYPES>
      <SPEC-OBJECT-TYPE IDENTIFIER="T-req" LONG-NAME="Requirement">
        <SPEC-ATTRIBUTES>
          <ATTRIBUTE-DEFINITION-XHTML IDENTIFIER="A-text" LONG-NAME="ReqIF.Text"/>
          <ATTRIBUTE-DEFINITION-STRING IDENTIFIER="A-name" LONG-NAME="ReqIF.Name"/>
          <ATTRIBUTE-DEFINITION-STRING IDENTIFIER="A-fid" LONG-NAME="ReqIF.ForeignID"/>
          <ATTRIBUTE-DEFINITION-STRING IDENTIFIER="A-chap" LONG-NAME="ReqIF.ChapterName"/>
        </SPEC-ATTRIBUTES>
      </SPEC-OBJECT-TYPE>
      <SPEC-RELATION-TYPE IDENTIFIER="RT-ref" LONG-NAME="refines"/>
      <SPEC-RELATION-TYPE IDENTIFIER="RT-tr" LONG-NAME="relates to"/>
    </SPEC-TYPES>
    <SPEC-OBJECTS>
      <SPEC-OBJECT IDENTIFIER="o-head" LONG-NAME="">
        <VALUES>
          <ATTRIBUTE-VALUE-STRING THE-VALUE="Installation"><DEFINITION><ATTRIBUTE-DEFINITION-STRING-REF>A-chap</ATTRIBUTE-DEFINITION-STRING-REF></DEFINITION></ATTRIBUTE-VALUE-STRING>
        </VALUES>
      </SPEC-OBJECT>
      <SPEC-OBJECT IDENTIFIER="o-1" LONG-NAME="fallback name">
        <VALUES>
          <ATTRIBUTE-VALUE-STRING THE-VALUE="SDV_101"><DEFINITION><ATTRIBUTE-DEFINITION-STRING-REF>A-fid</ATTRIBUTE-DEFINITION-STRING-REF></DEFINITION></ATTRIBUTE-VALUE-STRING>
          <ATTRIBUTE-VALUE-XHTML>
            <DEFINITION><ATTRIBUTE-DEFINITION-XHTML-REF>A-text</ATTRIBUTE-DEFINITION-XHTML-REF></DEFINITION>
            <THE-VALUE><xhtml:div><xhtml:p>Installing an update shall be <xhtml:b>quick</xhtml:b>.</xhtml:p></xhtml:div></THE-VALUE>
          </ATTRIBUTE-VALUE-XHTML>
        </VALUES>
      </SPEC-OBJECT>
      <SPEC-OBJECT IDENTIFIER="o-2">
        <VALUES>
          <ATTRIBUTE-VALUE-STRING THE-VALUE="SDV_102"><DEFINITION><ATTRIBUTE-DEFINITION-STRING-REF>A-fid</ATTRIBUTE-DEFINITION-STRING-REF></DEFINITION></ATTRIBUTE-VALUE-STRING>
          <ATTRIBUTE-VALUE-STRING THE-VALUE="Install time"><DEFINITION><ATTRIBUTE-DEFINITION-STRING-REF>A-name</ATTRIBUTE-DEFINITION-STRING-REF></DEFINITION></ATTRIBUTE-VALUE-STRING>
          <ATTRIBUTE-VALUE-XHTML>
            <DEFINITION><ATTRIBUTE-DEFINITION-XHTML-REF>A-text</ATTRIBUTE-DEFINITION-XHTML-REF></DEFINITION>
            <THE-VALUE><xhtml:div><xhtml:p>Installing shall take</xhtml:p><xhtml:p>at most 20 min.</xhtml:p></xhtml:div></THE-VALUE>
          </ATTRIBUTE-VALUE-XHTML>
        </VALUES>
      </SPEC-OBJECT>
    </SPEC-OBJECTS>
    <SPEC-RELATIONS>
      <SPEC-RELATION IDENTIFIER="r-1">
        <TYPE><SPEC-RELATION-TYPE-REF>RT-ref</SPEC-RELATION-TYPE-REF></TYPE>
        <SOURCE><SPEC-OBJECT-REF>o-2</SPEC-OBJECT-REF></SOURCE>
        <TARGET><SPEC-OBJECT-REF>o-1</SPEC-OBJECT-REF></TARGET>
      </SPEC-RELATION>
      <SPEC-RELATION IDENTIFIER="r-2">
        <TYPE><SPEC-RELATION-TYPE-REF>RT-tr</SPEC-RELATION-TYPE-REF></TYPE>
        <SOURCE><SPEC-OBJECT-REF>o-1</SPEC-OBJECT-REF></SOURCE>
        <TARGET><SPEC-OBJECT-REF>o-2</SPEC-OBJECT-REF></TARGET>
      </SPEC-RELATION>
    </SPEC-RELATIONS>
    <SPECIFICATIONS>
      <SPECIFICATION IDENTIFIER="s-1" LONG-NAME="Update manager SRS">
        <CHILDREN>
          <SPEC-HIERARCHY IDENTIFIER="h-1"><OBJECT><SPEC-OBJECT-REF>o-head</SPEC-OBJECT-REF></OBJECT>
            <CHILDREN>
              <SPEC-HIERARCHY IDENTIFIER="h-2"><OBJECT><SPEC-OBJECT-REF>o-1</SPEC-OBJECT-REF></OBJECT></SPEC-HIERARCHY>
              <SPEC-HIERARCHY IDENTIFIER="h-3"><OBJECT><SPEC-OBJECT-REF>o-2</SPEC-OBJECT-REF></OBJECT></SPEC-HIERARCHY>
            </CHILDREN>
          </SPEC-HIERARCHY>
        </CHILDREN>
      </SPECIFICATION>
    </SPECIFICATIONS>
  </REQ-IF-CONTENT></CORE-CONTENT>
</REQ-IF>`

func TestReadReqIF(t *testing.T) {
	src, err := ReadReqIF(strings.NewReader(sampleReqIF), "srs.reqif")
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{ExternalID: "SDV_101", Name: "fallback name", Text: "Installing an update shall be quick."},
		{ExternalID: "SDV_102", Name: "Install time", Text: "Installing shall take\nat most 20 min.", Refines: []string{"SDV_101"}},
	}
	if len(src.Items) != len(want) {
		t.Fatalf("items = %+v", src.Items)
	}
	for i := range want {
		g, w := src.Items[i], want[i]
		if g.ExternalID != w.ExternalID || g.Name != w.Name || g.Text != w.Text || !slices.Equal(g.Refines, w.Refines) {
			t.Errorf("item %d = %+v, want %+v", i, g, w)
		}
	}
	if len(src.Groups) != 1 || src.Groups[0].Name != "Update manager SRS" || !slices.Equal(src.Groups[0].Items, []string{"SDV_101", "SDV_102"}) {
		t.Errorf("groups = %+v", src.Groups)
	}
	skipped := strings.Join(src.Skipped, "\n")
	for _, w := range []string{`heading "Installation"`, `1 relations of type "relates to" not imported`} {
		if !strings.Contains(skipped, w) {
			t.Errorf("Skipped lacks %q:\n%s", w, skipped)
		}
	}
}

func TestReadReqIFZ(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.reqifz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("export.reqif")
	w.Write([]byte(sampleReqIF))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	src, err := ReadReqIFZ(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Items) != 2 || !strings.HasSuffix(src.Location, "!export.reqif") {
		t.Errorf("src = %+v", src)
	}
}

const sampleNeeds = `{
  "current_version": "1.0",
  "project": "demo",
  "versions": {
    "1.0": {
      "needs": {
        "REQ_002": {"id": "REQ_002", "title": "Install time", "content": "Installing shall take at most 20 min.", "type": "req", "docname": "srs/update", "refines": ["REQ_001"], "links": ["SPEC_001"]},
        "REQ_001": {"id": "REQ_001", "title": "Install quickly", "description": "Installing shall be quick.", "type": "req", "docname": "srs/update"},
        "SPEC_001": {"id": "SPEC_001", "title": "A spec", "content": "x", "type": "spec", "docname": "srs/update"},
        "REQ_003": {"id": "REQ_003", "title": "Empty", "content": "", "type": "req", "docname": "srs/other"}
      }
    }
  }
}`

func TestReadNeeds(t *testing.T) {
	src, err := ReadNeeds(strings.NewReader(sampleNeeds), "needs.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Items) != 2 || src.Items[0].ExternalID != "REQ_001" || src.Items[0].Text != "Installing shall be quick." ||
		!slices.Equal(src.Items[1].Refines, []string{"REQ_001"}) {
		t.Errorf("items = %+v", src.Items)
	}
	if len(src.Groups) != 1 || !slices.Equal(src.Groups[0].Items, []string{"REQ_001", "REQ_002"}) {
		t.Errorf("groups = %+v", src.Groups)
	}
	skipped := strings.Join(src.Skipped, "\n")
	if !strings.Contains(skipped, "REQ_003: no content") || !strings.Contains(skipped, "1 generic links") {
		t.Errorf("Skipped = %s", skipped)
	}
}

func model(t *testing.T) *req.Model {
	t.Helper()
	m := req.NewModel()
	m.PutScheme(&req.Scheme{ID: "status", Concepts: []req.Concept{{ID: "proposed"}, {ID: "approved"}}})
	m.PutScheme(&req.Scheme{ID: "requirement-context", Concepts: []req.Concept{{ID: "product"}, {ID: "tool"}}})
	m.Put(&req.Requirement{Element: req.Element{ID: "PLR-SDV-101"}, Text: "taken", Context: "product"}) // ID collision
	return m
}

func TestMergeAndReimport(t *testing.T) {
	m := model(t)
	src, err := ReadReqIF(strings.NewReader(sampleReqIF), "srs.reqif")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Merge(m, src, Options{Prefix: "PLR-"}); err == nil {
		t.Error("Merge without a context: want an error")
	}
	res, err := Merge(m, src, Options{Prefix: "PLR-", Context: "product"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range res.Created {
		ids = append(ids, req.Base(e).ID)
		m.Put(e)
	}
	if want := []string{"PLR-SDV-101-2", "PLR-SDV-102", "PLR-s-1"}; !slices.Equal(ids, want) {
		t.Fatalf("created %v, want %v", ids, want)
	}
	if is := m.Validate(); len(is) > 0 {
		t.Fatalf("Validate after import: %v", is)
	}
	r2, _ := m.Get("PLR-SDV-102")
	if got := r2.(*req.Requirement); !slices.Equal(got.Refines, []req.Ref{"PLR-SDV-101-2"}) || got.Provenance.ExternalID != "SDV_102" {
		t.Errorf("PLR-SDV-102 = %+v", got)
	}
	spec, _ := m.Get("PLR-s-1")
	if got := spec.(*req.Specification).Contains; !slices.Equal(got, []req.Ref{"PLR-SDV-101-2", "PLR-SDV-102"}) {
		t.Errorf("spec contains %v", got)
	}

	// Approve one, then re-import with its text changed in the tool.
	r1, _ := m.Get("PLR-SDV-101-2")
	ap := r1.(*req.Requirement)
	ap.Status = "approved"
	ap.Provenance.ApprovedBy = "SH-x"
	ap.Operationalizes = []req.Ref{"G-1"} // added in this model; must survive
	changed := strings.Replace(sampleReqIF, "shall be <xhtml:b>quick</xhtml:b>", "shall be fast", 1)
	src, err = ReadReqIF(strings.NewReader(changed), "srs.reqif")
	if err != nil {
		t.Fatal(err)
	}
	res, err = Merge(m, src, Options{Prefix: "PLR-", Context: "product"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Created) != 0 || len(res.Updated) != 1 {
		t.Fatalf("re-import created %d, updated %d", len(res.Created), len(res.Updated))
	}
	u := res.Updated[0].(*req.Requirement)
	if u.ID != "PLR-SDV-101-2" || u.Text != "Installing an update shall be fast." || u.Status != "proposed" ||
		u.Provenance.ApprovedBy != "" || !slices.Equal(u.Operationalizes, []req.Ref{"G-1"}) {
		t.Errorf("updated = %+v %+v", u, u.Provenance)
	}
	if ap.Status != "approved" {
		t.Error("Merge changed the model's own element instead of a copy")
	}

	// Saving writes files that load back.
	dir := t.TempDir()
	if err := res.Save(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "requirements", "PLR-SDV-101-2.json")); err != nil {
		t.Error(err)
	}
}

func TestSanitizedIDs(t *testing.T) {
	m := req.NewModel()
	src := &Source{Tool: "sphinx-needs", Items: []Item{
		{ExternalID: "REQ_001", Text: "a"},
		{ExternalID: "123", Text: "b"},
		{ExternalID: "ÄÖ", Text: "c"},
	}}
	res, err := Merge(m, src, Options{Context: "product"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range res.Created {
		ids = append(ids, req.Base(e).ID)
	}
	if want := []string{"REQ-001", "X123", "X"}; !slices.Equal(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
	if _, err := Merge(m, src, Options{Context: "product", Prefix: "1-"}); err == nil {
		t.Error("prefix starting with a digit: want an error")
	}
}
