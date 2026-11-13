package assist

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vanDeventer/Requirements/req"
)

type memSource struct{ m *req.Model }

func (s memSource) Read(f func(*req.Model)) { f(s.m) }

func toolbox(t *testing.T, opt Options) (*Toolbox, *Proposals) {
	t.Helper()
	m, err := req.Load("../example/ota")
	if err != nil {
		t.Fatal(err)
	}
	ps := NewProposals(t.TempDir())
	return NewToolbox(memSource{m}, ps, opt), ps
}

func call(t *testing.T, tb *Toolbox, name, args string) (string, bool) {
	t.Helper()
	return tb.Call(context.Background(), name, json.RawMessage(args))
}

func TestTools(t *testing.T) {
	tb, _ := toolbox(t, Options{})
	for _, c := range []struct {
		name, args string
		isErr      bool
		wants      []string
	}{
		{"get", `{"id":"REQ-001"}`, false, []string{`"kind":"Requirement"`, `parking brake`}},
		{"get", `{"id":"NOPE"}`, true, []string{"does not exist"}},
		{"get", `{"id":"REQ-001","extra":1}`, true, []string{"bad arguments"}},
		{"find", `{"query":"parking brake"}`, false, []string{`"REQ-001"`}},
		{"find", `{"query":"zzz"}`, false, []string{`[]`}},
		{"list", `{"kind":"Hazard"}`, false, []string{`"HAZ-001"`, `"HAZ-002"`}},
		{"list", `{"kind":"Nope"}`, true, []string{"unknown kind", "Requirement"}},
		{"get", `{}`, true, []string{"missing required arguments: id"}},
		{"get", `{"id":""}`, true, []string{"missing required arguments: id"}},
		// Seen from qwen3:14b: propose without a kind used to panic.
		{"propose", `{"summary":"x","element":{"id":"REQ-005"}}`, true, []string{"missing required arguments: kind"}},
		{"propose", `{"kind":"Nope","summary":"x","element":{"id":"REQ-005"}}`, true, []string{"unknown kind"}},
		{"trace", `{"id":"REQ-001"}`, false, []string{`"rel":"operationalizes","to":"G-002"`, `"via":"SAT-001"`}},
		{"impact", `{"id":"AS-001"}`, false, []string{`"direct":["REQ-001"]`}},
		{"evidence", `{"id":"REQ-002"}`, false, []string{`"verified":false`, "VER-002 is fail"}},
		{"unverified", `{}`, false, []string{`"REQ-002"`, `"REQ-005"`}},
		{"unverified", `{"spec":"SPEC-001"}`, false, []string{`"REQ-004"`}},
		{"coverage", `{"id":"CL-R156-7-2"}`, false, []string{`"covered":false`}},
		{"check", `{}`, false, []string{`"errors":[]`, "no acceptance criterion"}},
		{"nope", `{}`, true, []string{"unknown tool"}},
	} {
		got, isErr := call(t, tb, c.name, c.args)
		if isErr != c.isErr {
			t.Errorf("%s %s: isError %v: %s", c.name, c.args, isErr, got)
		}
		for _, w := range c.wants {
			if !strings.Contains(got, w) {
				t.Errorf("%s %s: lacks %q in %s", c.name, c.args, w, got)
			}
		}
	}
	// Every tool's schema is valid JSON with an object type.
	for _, tool := range tb.Tools() {
		var s map[string]any
		if err := json.Unmarshal(tool.Params, &s); err != nil || s["type"] != "object" {
			t.Errorf("%s: bad schema %s", tool.Name, tool.Params)
		}
	}
	if len(tb.Tools()) != 11 {
		t.Errorf("%d tools without sparql, want 11", len(tb.Tools()))
	}
}

func TestPropose(t *testing.T) {
	tb, ps := toolbox(t, Options{Model: "test-model"})
	got, isErr := call(t, tb, "propose", `{"kind":"Assumption","summary":"Network availability matters for OTA.","sources":["https://example.org/net"],
		"element":{"id":"AS-003","name":"Network available","kind":"expectation","text":"The vehicle has cellular coverage at least once a day."}}`)
	if isErr || !strings.Contains(got, `"proposal":"P-1"`) {
		t.Fatalf("propose: %s", got)
	}
	for _, c := range []struct{ args, want string }{
		{`{"kind":"Requirement","summary":"x","element":{"id":"REQ-001","text":"x","context":"product","operationalizes":["G-404"]}}`, "G-404 does not exist"},
		{`{"kind":"Verification","summary":"x","element":{"id":"VER-9"}}`, "can't be proposed"},
		{`{"kind":"Goal","summary":"x","element":{"id":"REQ-001"}}`, "already exists as a Requirement"},
		{`{"kind":"Requirement","summary":"x","element":{"id":"REQ-9","txet":"x"}}`, "doesn't decode"},
		{`{"kind":"Assumption","summary":" ","element":{"id":"AS-4","text":"x"}}`, "summary is required"},
	} {
		got, isErr := call(t, tb, "propose", c.args)
		if !isErr || !strings.Contains(got, c.want) {
			t.Errorf("propose %s: %v %s", c.args, isErr, got)
		}
	}
	list, err := ps.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("proposals: %v %v", list, err)
	}
	p := list[0]
	if p.ID != "P-1" || !p.New || p.Model != "test-model" || p.Kind != req.KindAssumption || p.Sources[0] != "https://example.org/net" {
		t.Errorf("proposal = %+v", p)
	}
	// Numbers are never reused, even after every proposal is removed.
	call(t, tb, "propose", `{"kind":"Assumption","summary":"y","element":{"id":"AS-5","text":"y"}}`)
	if err := ps.Remove("P-1"); err != nil {
		t.Fatal(err)
	}
	ps.Remove("P-2")
	got, _ = call(t, tb, "propose", `{"kind":"Assumption","summary":"z","element":{"id":"AS-6","text":"z"}}`)
	if !strings.Contains(got, `"P-3"`) {
		t.Errorf("third proposal: %s", got)
	}
	for _, bad := range []string{"../x", "P-0", "P-01", "x"} {
		if _, err := ps.Get(bad); err == nil {
			t.Errorf("Get(%q): want an error", bad)
		}
	}
}

func rpc(t *testing.T, tb *Toolbox, msg string) map[string]any {
	t.Helper()
	res := tb.handle(context.Background(), []byte(msg))
	if res == nil {
		return nil
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

func TestMCP(t *testing.T) {
	tb, _ := toolbox(t, Options{})
	init := rpc(t, tb, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	r := init["result"].(map[string]any)
	if r["protocolVersion"] != "2025-03-26" || r["capabilities"].(map[string]any)["tools"] == nil {
		t.Errorf("initialize = %v", init)
	}
	if v := rpc(t, tb, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"1999-01-01"}}`)["result"].(map[string]any)["protocolVersion"]; v != protocolVersions[0] {
		t.Errorf("unknown version negotiated to %v", v)
	}
	if rpc(t, tb, `{"jsonrpc":"2.0","method":"notifications/initialized"}`) != nil {
		t.Error("a notification got a response")
	}
	list := rpc(t, tb, `{"jsonrpc":"2.0","id":"a","method":"tools/list"}`)
	if n := len(list["result"].(map[string]any)["tools"].([]any)); n != 11 || list["id"] != "a" {
		t.Errorf("tools/list: %d tools, id %v", n, list["id"])
	}
	res := rpc(t, tb, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"evidence","arguments":{"id":"REQ-001"}}}`)["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if res["isError"] != false || !strings.Contains(text, `"verified":true`) {
		t.Errorf("tools/call = %v", res)
	}
	res = rpc(t, tb, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"get","arguments":{"id":"NOPE"}}}`)["result"].(map[string]any)
	if res["isError"] != true {
		t.Errorf("tool error not flagged: %v", res)
	}
	for msg, code := range map[string]float64{
		`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`: -32601,
		`not json`: -32700,
		`{"jsonrpc":"1.0","id":6,"method":"ping"}`:                   -32600,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{}}`: -32602,
	} {
		if got := rpc(t, tb, msg)["error"].(map[string]any)["code"]; got != code {
			t.Errorf("%s: code %v, want %v", msg, got, code)
		}
	}
}

func TestMCPTransports(t *testing.T) {
	tb, _ := toolbox(t, Options{})
	srv := httptest.NewServer(tb)
	defer srv.Close()
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(string(b), `"result":{}`) {
		t.Errorf("POST ping: %d %s", resp.StatusCode, b)
	}
	resp, _ = http.Post(srv.URL, "application/json", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("notification: %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", resp.StatusCode)
	}

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}
{"jsonrpc":"2.0","method":"notifications/initialized"}

{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"find","arguments":{"query":"night"}}}
`)
	var out bytes.Buffer
	if err := tb.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `REQ-007`) {
		t.Errorf("stdio output: %s", out.String())
	}
}

func TestSPARQL(t *testing.T) {
	var gotQuery string
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotQuery = r.FormValue("query")
		w.Header().Set("Content-Type", "application/sparql-results+json")
		if strings.HasPrefix(gotQuery, "ASK") {
			io.WriteString(w, `{"head":{},"boolean":true}`)
			return
		}
		if strings.Contains(gotQuery, "broken") {
			http.Error(w, "MALFORMED QUERY", http.StatusBadRequest)
			return
		}
		io.WriteString(w, `{"head":{"vars":["x"]},"results":{"bindings":[{"x":{"type":"uri","value":"https://example.org/requirements/REQ-001"}}]}}`)
	}))
	defer store.Close()
	tb, _ := toolbox(t, Options{SPARQL: store.URL})
	got, isErr := call(t, tb, "sparql", `{"query":"SELECT ?x WHERE { ?x ?p ?o }"}`)
	if isErr || !strings.Contains(got, `"rows":[{"x":"https://example.org/requirements/REQ-001"}]`) || gotQuery != "SELECT ?x WHERE { ?x ?p ?o }" {
		t.Errorf("select: %s (query %q)", got, gotQuery)
	}
	if got, _ := call(t, tb, "sparql", `{"query":"ASK { ?s ?p ?o }"}`); got != `{"answer":true}` {
		t.Errorf("ask: %s", got)
	}
	if got, isErr := call(t, tb, "sparql", `{"query":"broken"}`); !isErr || !strings.Contains(got, "MALFORMED") {
		t.Errorf("error: %v %s", isErr, got)
	}
}

// fakeLLM answers like an OpenAI-compatible server: scripted replies, and it
// records the requests.
type fakeLLM struct {
	replies  []string
	requests []map[string]any
}

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/chat/completions" {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	f.requests = append(f.requests, body)
	reply := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	io.WriteString(w, `{"choices":[{"message":`+reply+`}]}`)
}

func TestReply(t *testing.T) {
	fake := &fakeLLM{replies: []string{
		`{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"evidence","arguments":"{\"id\":\"REQ-002\"}"}},{"id":"c2","type":"function","function":{"name":"get","arguments":"{\"id\":\"NOPE\"}"}}]}`,
		`{"role":"assistant","content":"<think>check evidence</think>REQ-002 is not verified: VER-002 failed."}`,
	}}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	tb, _ := toolbox(t, Options{})
	llm := &LLM{URL: srv.URL + "/v1", Model: "fake"}
	added, err := llm.Reply(context.Background(), tb, nil, "Is REQ-002 verified?")
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 5 || added[0].Role != "user" || added[1].Role != "assistant" || added[2].Role != "tool" || added[4].Content != "REQ-002 is not verified: VER-002 failed." {
		t.Fatalf("added = %+v", added)
	}
	if !strings.Contains(added[2].Content, "VER-002 is fail") || added[2].IsError || !added[3].IsError {
		t.Errorf("tool results = %+v %+v", added[2], added[3])
	}
	// The second request carried the system prompt, the tools and the results.
	second := fake.requests[1]
	msgs := second["messages"].([]any)
	if msgs[0].(map[string]any)["role"] != "system" || len(second["tools"].([]any)) != 11 || second["model"] != "fake" {
		t.Errorf("request: %v", second)
	}
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" || last["tool_call_id"] != "c2" {
		t.Errorf("last message: %v", last)
	}

	// A model that never stops calling tools is cut off.
	loop := &fakeLLM{replies: []string{`{"role":"assistant","tool_calls":[{"id":"x","type":"function","function":{"name":"check","arguments":"{}"}}]}`}}
	srv2 := httptest.NewServer(loop)
	defer srv2.Close()
	if _, err := (&LLM{URL: srv2.URL + "/v1", Model: "m"}).Reply(context.Background(), tb, nil, "?"); err == nil || !strings.Contains(err.Error(), "rounds") {
		t.Errorf("endless tool calls: %v", err)
	}
	if _, err := (&LLM{URL: "http://127.0.0.1:1/v1", Model: "m"}).Reply(context.Background(), tb, nil, "?"); err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("no server: %v", err)
	}
}

func TestDirSource(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "m")
	if err := os.CopyFS(dir, os.DirFS("../example/ota")); err != nil {
		t.Fatal(err)
	}
	tb := NewToolbox(DirSource(dir), NewProposals(dir), Options{})
	os.Remove(filepath.Join(dir, "verifications", "VER-001.json"))
	got, _ := call(t, tb, "evidence", `{"id":"REQ-001"}`)
	if !strings.Contains(got, `"verified":false`) {
		t.Errorf("DirSource didn't see the change on disk: %s", got)
	}
	call(t, tb, "propose", `{"kind":"Assumption","summary":"s","element":{"id":"AS-7","text":"t"}}`)
	if _, err := req.Load(dir); err != nil {
		t.Errorf("proposals disturb loading the model: %v", err)
	}
}

func TestProposeReportsRemovedFields(t *testing.T) {
	tb, ps := toolbox(t, Options{})
	// REQ-005 has a rationale; this rewrite drops it and adds a criterion.
	got, isErr := call(t, tb, "propose", `{"kind":"Requirement","summary":"Add a criterion.","element":{"id":"REQ-005","name":"Switch only after integrity check","status":"proposed","text":"The update manager shall switch to the newly written partition only after its integrity check passes.","context":"product","form":"functional","concerns":["security"],"derivedFrom":["DE-002"],"security":{"cal":"CAL2","threatScenario":"TS-001"},"criteria":[{"metric":"switches-without-integrity-check","operator":"=","threshold":0}]}}`)
	if isErr || !strings.Contains(got, `"removed":["rationale"]`) || !strings.Contains(got, `"added":["criteria"]`) || !strings.Contains(got, "removes rationale") {
		t.Errorf("propose = %s", got)
	}
	// Stored in the element's own field order.
	list, _ := ps.List()
	var compact bytes.Buffer
	json.Compact(&compact, list[0].Element)
	if !strings.HasPrefix(compact.String(), `{"id":"REQ-005","name":`) {
		t.Errorf("stored element = %s", list[0].Element)
	}
	// Proposing the element unchanged is refused.
	cur, _ := call(t, tb, "get", `{"id":"AS-001"}`)
	var g struct{ Element json.RawMessage }
	json.Unmarshal([]byte(cur), &g)
	if got, isErr := call(t, tb, "propose", `{"kind":"Assumption","summary":"same","element":`+string(g.Element)+`}`); !isErr || !strings.Contains(got, "unchanged") {
		t.Errorf("unchanged proposal: %s", got)
	}
}

func TestDiff(t *testing.T) {
	c := Diff([]byte(`{"id":"A","name":"x","list":[1, 2],"gone":true}`), []byte(`{"id":"A","name":"y","list":[1,2],"new":1}`))
	if strings.Join(c.Changed, ",") != "name" || strings.Join(c.Removed, ",") != "gone" || strings.Join(c.Added, ",") != "new" {
		t.Errorf("Diff = %+v", c)
	}
	if c := Diff(nil, []byte(`{"id":"A"}`)); strings.Join(c.Added, ",") != "id" {
		t.Errorf("Diff of new = %+v", c)
	}
}

func TestDescribe(t *testing.T) {
	tb, _ := toolbox(t, Options{})
	got, isErr := call(t, tb, "describe", `{"kind":"Requirement"}`)
	if isErr {
		t.Fatal(got)
	}
	for _, w := range []string{
		`{"field":"text","type":"string","multiline":true,"required":true,"help":"The requirement itself`,
		`{"field":"context","type":"concept","required":true,"values":["product","tool","training-data"]`,
		`{"field":"operationalizes","type":"element ID","list":true,"refersTo":["Goal"]`,
		`"field":"criteria","type":"object","list":true`,
		`{"field":"metric","type":"concept","values":["installs-started-while-moving"`,
		`{"field":"threshold","type":"number"`,
		`{"field":"approvedOn","type":"date-time"`,
		`"suggestedID":"REQ-008"`,
		"needs the concern safety",
	} {
		if !strings.Contains(got, w) {
			t.Errorf("describe lacks %s", w)
		}
	}
	got, _ = call(t, tb, "propose", `{"kind":"Requirement","summary":"x","element":{"id":"REQ-005","acceptanceCriterion":{}}}`)
	if !strings.Contains(got, `unknown field "acceptanceCriterion"`) || !strings.Contains(got, "criteria") || !strings.Contains(got, "Call describe") {
		t.Errorf("decode error lacks a hint: %s", got)
	}
}

func TestToolPanicBecomesError(t *testing.T) {
	tb, _ := toolbox(t, Options{})
	tb.tools = append(tb.tools, Tool{Name: "boom", Params: schema(``), run: func(context.Context, json.RawMessage) (any, error) {
		var m map[string]int
		m["x"] = 1
		return nil, nil
	}})
	got, isErr := call(t, tb, "boom", `{}`)
	if !isErr || !strings.Contains(got, "internal error in boom") {
		t.Errorf("panic: %v %s", isErr, got)
	}
}
