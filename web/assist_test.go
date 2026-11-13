package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vanDeventer/Requirements/assist"
	"github.com/vanDeventer/Requirements/req"
)

func TestMCPEndpoint(t *testing.T) {
	s, _ := newServerWith(t, false, Options{LocalOnly: true})
	send := func(host string, hdr ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"coverage","arguments":{"id":"HAZ-001"}}}`))
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := send("127.0.0.1:8080"); w.Code != 200 || !strings.Contains(w.Body.String(), `\"covered\":true`) {
		t.Errorf("MCP client: %d %s", w.Code, w.Body)
	}
	if w := send("localhost:8080"); w.Code != 200 {
		t.Errorf("localhost: %d", w.Code)
	}
	if w := send("[::1]:8080"); w.Code != 200 {
		t.Errorf("::1: %d", w.Code)
	}
	if w := send("evil.example:8080"); w.Code != http.StatusForbidden {
		t.Errorf("rebinding host: %d", w.Code)
	}
	if w := send("localhost:8080", "Origin", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-origin browser POST: %d", w.Code)
	}
}

func TestProposals(t *testing.T) {
	s, dir := newServer(t, false)
	text, isErr := s.tools.Call(t.Context(), "propose", json.RawMessage(`{"kind":"Requirement","summary":"Add a criterion so REQ-005 can be verified.","sources":["https://example.org/ab-design"],
		"element":{"id":"REQ-005","name":"Switch only after integrity check","status":"proposed","text":"The update manager shall switch to the newly written partition only after its integrity check passes.","context":"product","form":"functional","concerns":["security"],"derivedFrom":["DE-002"],"security":{"cal":"CAL2","threatScenario":"TS-001"},
		"criteria":[{"metric":"switches-without-integrity-check","operator":"=","threshold":0}]}}`))
	if isErr {
		t.Fatal(text)
	}
	_, layout := get(t, s, "/")
	mustContain(t, "nav", layout, `<span class="badge warn">1</span>`)
	code, page := get(t, s, "/proposals")
	if code != 200 {
		t.Fatalf("/proposals = %d", code)
	}
	mustContain(t, "proposals page", page, "P-1", "Add a criterion so REQ-005 can be verified.", "switches-without-integrity-check", `<option value="SH-safety">`,
		"Adds <code>criteria</code>", "Removes <code>rationale</code>")

	if w := post(t, s, "/proposals/P-1/approve", url.Values{"approver": {""}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("approve without approver = %d", w.Code)
	}
	if w := post(t, s, "/proposals/P-1/approve", url.Values{"approver": {"REQ-001"}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("approve by a non-stakeholder = %d", w.Code)
	}
	w := post(t, s, "/proposals/P-1/approve", url.Values{"approver": {"SH-sw-lead"}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/e/REQ-005" {
		t.Fatalf("approve = %d %s", w.Code, w.Body)
	}
	m, err := req.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := m.Get("REQ-005")
	r := e.(*req.Requirement)
	p := r.Provenance
	if len(r.Criteria) != 1 || p == nil || p.GeneratedBy != "language model" || p.ApprovedBy != "SH-sw-lead" || p.ApprovedOn.IsZero() || p.Sources[0] != "https://example.org/ab-design" {
		t.Errorf("saved REQ-005 = %+v %+v", r, p)
	}
	for _, g := range m.Gaps() {
		if g.ID == "REQ-005" {
			t.Errorf("gap left after approval: %v", g)
		}
	}
	if list, _ := s.proposals.List(); len(list) != 0 {
		t.Errorf("proposal not removed: %v", list)
	}

	// Reject removes without changing the model.
	s.tools.Call(t.Context(), "propose", json.RawMessage(`{"kind":"Assumption","summary":"s","element":{"id":"AS-8","text":"t"}}`))
	if w := post(t, s, "/proposals/P-2/reject", nil); w.Code != http.StatusSeeOther {
		t.Errorf("reject = %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "assumptions", "AS-8.json")); !os.IsNotExist(err) {
		t.Error("a rejected proposal was saved")
	}
	if w := post(t, s, "/proposals/P-9/approve", url.Values{"approver": {"SH-sw-lead"}}); w.Code != http.StatusNotFound {
		t.Errorf("approve missing = %d", w.Code)
	}
	if w := post(t, s, "/proposals/..%2Fx/reject", nil); w.Code == http.StatusSeeOther {
		t.Errorf("path in proposal id accepted")
	}
}

func TestChat(t *testing.T) {
	s, _ := newServer(t, false)
	code, body := get(t, s, "/chat")
	if code != 200 || !strings.Contains(body, "-llm-model") {
		t.Errorf("chat off: %d", code)
	}
	if w := post(t, s, "/chat", url.Values{"message": {"hi"}}); w.Code != http.StatusNotFound {
		t.Errorf("POST /chat with chat off = %d", w.Code)
	}

	turn := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		turn++
		if turn == 1 {
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"coverage","arguments":"{\"id\":\"CL-R156-7-2\"}"}}]}}]}`)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"CL-R156-7-2 is not covered: REQ-002 failed VER-002. <b>bold</b>"}}]}`)
	}))
	defer model.Close()
	s, _ = newServerWith(t, false, Options{LLM: &assist.LLM{URL: model.URL, Model: "fake-model"}})
	w := post(t, s, "/chat", url.Values{"message": {"Is the R156 clause covered?"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST /chat = %d %s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	code, body = get(t, s, strings.TrimSuffix(loc, "#end"))
	if code != 200 {
		t.Fatalf("GET %s = %d", loc, code)
	}
	mustContain(t, "chat page", body,
		"Is the R156 clause covered?",
		`<a href="/e/CL-R156-7-2">CL-R156-7-2</a> is not covered`,
		`<a href="/e/VER-002">VER-002</a>`,
		"&lt;b&gt;bold&lt;/b&gt;",
		"<code>coverage</code>",
		`&#34;covered&#34;:false`, // the tool result, escaped
		"fake-model")
}
