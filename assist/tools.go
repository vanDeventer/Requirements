// Package assist lets a language model work with a requirements model: a
// toolbox of typed queries, proposals that a person approves, an MCP server
// over HTTP or stdio, and a chat loop against a local OpenAI-compatible
// model server.
//
// The model answers from the tools and cites element IDs; verdicts (satisfied,
// verified, covered, gaps) always come from the deterministic queries. It
// changes nothing directly: propose stores a pending change, which a person
// approves or rejects in the web UI.
package assist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

// Source gives read access to the current model. The function must not keep
// the model after it returns.
type Source interface {
	Read(func(m *req.Model))
}

// DirSource loads the model from a directory on every read, so a separate
// process (an MCP server over stdio) sees edits made in the web UI.
type DirSource string

func (d DirSource) Read(f func(*req.Model)) {
	m, err := req.Load(string(d))
	if err != nil && m == nil {
		m = req.NewModel()
	}
	f(m)
}

// Tool is one function the language model can call.
type Tool struct {
	Name        string
	Description string
	Params      json.RawMessage // JSON Schema of the arguments
	run         func(ctx context.Context, args json.RawMessage) (any, error)
}

// Toolbox holds the tools for one model.
type Toolbox struct {
	src       Source
	proposals *Proposals
	sparql    string // repository query endpoint; empty disables the tool
	model     string // name of the language model, recorded on proposals
	tools     []Tool
}

// Options configure a Toolbox.
type Options struct {
	SPARQL string // e.g. http://localhost:7200/repositories/req; empty: no sparql tool
	Model  string // language model name recorded on proposals, e.g. "qwen3:32b"
}

// NewToolbox returns the tools for src, storing proposals in ps.
func NewToolbox(src Source, ps *Proposals, opt Options) *Toolbox {
	tb := &Toolbox{src: src, proposals: ps, sparql: opt.SPARQL, model: opt.Model}
	tb.define()
	return tb
}

// Tools lists the tools, in a stable order.
func (tb *Toolbox) Tools() []Tool { return tb.tools }

// Call runs a tool. Errors that the model should see and correct (bad
// arguments, unknown IDs) are returned as error results, not Go errors. A
// panic in a tool also becomes an error result: input from a language model
// must never take the server down.
func (tb *Toolbox) Call(ctx context.Context, name string, args json.RawMessage) (result string, isError bool) {
	i := slices.IndexFunc(tb.tools, func(t Tool) bool { return t.Name == name })
	if i < 0 {
		return fmt.Sprintf("unknown tool %q", name), true
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage("{}")
	}
	if missing := missingRequired(tb.tools[i].Params, args); len(missing) > 0 {
		return fmt.Sprintf("missing required arguments: %s", strings.Join(missing, ", ")), true
	}
	defer func() {
		if p := recover(); p != nil {
			result, isError = fmt.Sprintf("internal error in %s: %v", name, p), true
		}
	}()
	v, err := tb.tools[i].run(ctx, args)
	if err != nil {
		return err.Error(), true
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error(), true
	}
	return string(b), false
}

// missingRequired lists the schema's required properties absent from args
// (or empty strings). The model sees the schema, but nothing obliges it to
// follow it.
func missingRequired(schema, args json.RawMessage) []string {
	var s struct {
		Required []string `json:"required"`
	}
	var a map[string]json.RawMessage
	if json.Unmarshal(schema, &s) != nil || json.Unmarshal(args, &a) != nil {
		return nil
	}
	var missing []string
	for _, r := range s.Required {
		if v, ok := a[r]; !ok || string(v) == `""` || string(v) == "null" {
			missing = append(missing, r)
		}
	}
	return missing
}

// kindError explains an unknown kind with the valid ones.
func kindError(k req.Kind) error {
	var ks []string
	for _, x := range req.Kinds() {
		ks = append(ks, string(x))
	}
	return fmt.Errorf("unknown kind %q; use one of %s", k, strings.Join(ks, ", "))
}

func schema(props string, required ...string) json.RawMessage {
	req := "[]"
	if len(required) > 0 {
		b, _ := json.Marshal(required)
		req = string(b)
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + req + `,"additionalProperties":false}`)
}

func decode(args json.RawMessage, v any) error {
	d := json.NewDecoder(strings.NewReader(string(args)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("bad arguments: %v", err)
	}
	return nil
}

type idArg struct {
	ID string `json:"id"`
}

const idProp = `"id":{"type":"string","description":"Element ID, e.g. REQ-001"}`

// read runs f on the model and returns its result.
func (tb *Toolbox) read(f func(m *req.Model) (any, error)) (v any, err error) {
	tb.src.Read(func(m *req.Model) { v, err = f(m) })
	return v, err
}

func (tb *Toolbox) define() {
	kinds := make([]string, 0)
	for _, k := range req.Kinds() {
		kinds = append(kinds, `"`+string(k)+`"`)
	}
	kindEnum := `{"type":"string","enum":[` + strings.Join(kinds, ",") + `]}`

	withID := func(f func(m *req.Model, id req.Ref) (any, error)) func(context.Context, json.RawMessage) (any, error) {
		return func(_ context.Context, args json.RawMessage) (any, error) {
			var a idArg
			if err := decode(args, &a); err != nil {
				return nil, err
			}
			return tb.read(func(m *req.Model) (any, error) { return f(m, req.Ref(a.ID)) })
		}
	}

	tb.tools = []Tool{
		{
			Name:        "get",
			Description: "Get one element by ID: its kind and all stored fields and links, e.g. what a specification contains, an element's owner, assumptions and provenance.",
			Params:      schema(idProp, "id"),
			run: withID(func(m *req.Model, id req.Ref) (any, error) {
				e, ok := m.Get(id)
				if !ok {
					return nil, fmt.Errorf("%s does not exist; use find", id)
				}
				return map[string]any{"kind": req.KindOf(e), "element": e}, nil
			}),
		},
		{
			Name:        "find",
			Description: "Find elements whose ID, name, description, text or statement contain all the given words, ignoring case.",
			Params:      schema(`"query":{"type":"string"}`, "query"),
			run: func(_ context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Query string `json:"query"`
				}
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				return tb.read(func(m *req.Model) (any, error) { return nonNil(m.Find(a.Query)), nil })
			},
		},
		{
			Name:        "list",
			Description: "List the elements of one kind with their names and statuses, and, for AI-generated content, the generating model and the approver.",
			Params:      schema(`"kind":`+kindEnum, "kind"),
			run: func(_ context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Kind req.Kind `json:"kind"`
				}
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				if req.New(a.Kind) == nil {
					return nil, kindError(a.Kind)
				}
				return tb.read(func(m *req.Model) (any, error) {
					type row struct {
						ID          string  `json:"id"`
						Name        string  `json:"name,omitempty"`
						Status      req.Ref `json:"status,omitempty"`
						GeneratedBy string  `json:"generatedBy,omitempty"`
						ApprovedBy  req.Ref `json:"approvedBy,omitempty"`
					}
					rows := []row{}
					for _, e := range m.Elems() {
						if req.KindOf(e) == a.Kind {
							b := req.Base(e)
							r := row{ID: b.ID, Name: b.Name, Status: b.Status}
							if p := b.Provenance; p != nil {
								r.GeneratedBy, r.ApprovedBy = p.GeneratedBy, p.ApprovedBy
							}
							rows = append(rows, r)
						}
					}
					return rows, nil
				})
			},
		},
		{
			Name: "trace",
			Description: "Trace an element: the links up toward its origin (goals, needs, hazards, ideas) and down toward the solution (subgoals, requirements, design, satisfaction claims, verifications). " +
				"It doesn't follow specification membership, owners or assumptions; use get or impact for those.",
			Params: schema(idProp, "id"),
			run: withID(func(m *req.Model, id req.Ref) (any, error) {
				t, err := m.Trace(id)
				if err != nil {
					return nil, err
				}
				return map[string]any{"root": t.Root, "up": edges(t.Up), "down": edges(t.Down)}, nil
			}),
		},
		{
			Name:        "impact",
			Description: "What must be reviewed if an element changes, or if an assumption turns out false: the elements depending on it directly, and everything below them.",
			Params:      schema(idProp, "id"),
			run:         withID(func(m *req.Model, id req.Ref) (any, error) { return m.Impact(id) }),
		},
		{
			Name:        "evidence",
			Description: "Whether a requirement is satisfied and verified, with the reasons. This is the only authority on verification status.",
			Params:      schema(idProp, "id"),
			run:         withID(func(m *req.Model, id req.Ref) (any, error) { return m.Evidence(id) }),
		},
		{
			Name:        "unverified",
			Description: "The active requirements that are not verified, with reasons; in one specification if spec is given, otherwise all.",
			Params:      schema(`"spec":{"type":"string","description":"Specification ID; omit for all requirements"}`),
			run: func(_ context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Spec string `json:"spec"`
				}
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				return tb.read(func(m *req.Model) (any, error) {
					es, err := m.Unverified(req.Ref(a.Spec))
					return nonNil(es), err
				})
			},
		},
		{
			Name:        "coverage",
			Description: "Whether a need, hazard, threat scenario or standard clause is covered: addressed by a goal, with every requirement below verified. For a clause this is the compliance check.",
			Params:      schema(idProp, "id"),
			run:         withID(func(m *req.Model, id req.Ref) (any, error) { return m.Coverage(id) }),
		},
		{
			Name:        "check",
			Description: "Structural errors and completeness gaps of the whole model.",
			Params:      schema(``),
			run: func(_ context.Context, args json.RawMessage) (any, error) {
				if err := decode(args, &struct{}{}); err != nil {
					return nil, err
				}
				return tb.read(func(m *req.Model) (any, error) {
					return map[string]any{"errors": nonNil(m.Validate()), "gaps": nonNil(m.Gaps())}, nil
				})
			},
		},
		{
			Name:        "describe",
			Description: "Describe the stored form of a kind of element: its fields with their types, which are required, which kinds a reference may point to, and the allowed values of concept fields. Call it before propose.",
			Params:      schema(`"kind":`+kindEnum, "kind"),
			run: func(_ context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Kind req.Kind `json:"kind"`
				}
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				if req.New(a.Kind) == nil {
					return nil, kindError(a.Kind)
				}
				return tb.read(func(m *req.Model) (any, error) { return describe(m, a.Kind), nil })
			},
		},
		{
			Name: "propose",
			Description: "Propose adding or changing one element. The element is the complete JSON of the element as stored (use get first when changing one). " +
				"Nothing changes until a person approves it in the web UI. Satisfaction claims and verifications can't be proposed: they record what people claimed and measured.",
			Params: schema(`"kind":`+kindEnum+`,"element":{"type":"object","description":"The complete element, including id"},"summary":{"type":"string","description":"One sentence: what changes and why"},"sources":{"type":"array","items":{"type":"string"},"description":"Documents or URLs the content was actually taken from, as named by the user or the model; leave out rather than guess"}`, "kind", "element", "summary"),
			run: func(_ context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Kind    req.Kind        `json:"kind"`
					Element json.RawMessage `json:"element"`
					Summary string          `json:"summary"`
					Sources []string        `json:"sources"`
				}
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				return tb.propose(a.Kind, a.Element, a.Summary, a.Sources)
			},
		},
	}
	if tb.sparql != "" {
		tb.tools = append(tb.tools, Tool{
			Name: "sparql",
			Description: "Run a read-only SPARQL SELECT or ASK query on the graph store, which holds the last export with the req: ontology. " +
				"Inferred properties: req:tracesTo (any trace path, transitive), req:servesOrigin (goal path to a need, hazard, threat or clause), req:contributesTo, inverses such as req:refinedBy. " +
				"Computed: req:isSatisfied, req:isVerified, req:isCovered. Element IRIs end in the ID; dcterms:identifier holds it. The graph may lag behind the model; for current verdicts use the other tools.",
			Params: schema(`"query":{"type":"string"}`, "query"),
			run: func(ctx context.Context, args json.RawMessage) (any, error) {
				var a struct {
					Query string `json:"query"`
				}
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				return tb.runSPARQL(ctx, a.Query)
			},
		})
	}
}

type edge struct {
	From req.Ref `json:"from"`
	Rel  string  `json:"rel"`
	To   req.Ref `json:"to"`
	Via  req.Ref `json:"via,omitempty"`
}

func edges(es []req.Edge) []edge {
	out := []edge{}
	for _, e := range es {
		out = append(out, edge{e.From, e.Rel, e.To, e.Via})
	}
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func (tb *Toolbox) propose(k req.Kind, element json.RawMessage, summary string, sources []string) (any, error) {
	if req.New(k) == nil {
		return nil, kindError(k)
	}
	if k == req.KindSatisfaction || k == req.KindVerification {
		return nil, fmt.Errorf("%s can't be proposed: satisfaction claims and verifications record what people claimed and measured", k)
	}
	e, err := req.Decode(k, element)
	if err != nil {
		return nil, fmt.Errorf("the element doesn't decode as a %s: %v. The fields of a %s are: %s. Call describe for their types and allowed values",
			k, err, k, strings.Join(fieldNames(k), ", "))
	}
	if strings.TrimSpace(summary) == "" {
		return nil, errors.New("a summary is required")
	}
	id := req.Ref(req.Base(e).ID)
	var problems []string
	var isNew bool
	var current []byte
	tb.src.Read(func(m *req.Model) {
		if old, ok := m.Get(id); ok && req.KindOf(old) != k {
			problems = []string{fmt.Sprintf("%s already exists as a %s", id, req.KindOf(old))}
			return
		} else if ok {
			current, _ = json.Marshal(old)
		} else {
			isNew = true
		}
		before := map[string]bool{}
		for _, i := range m.Validate() {
			before[i.String()] = true
		}
		c := m.Clone()
		c.Put(e)
		for _, i := range c.Validate() {
			if !before[i.String()] {
				problems = append(problems, i.String())
			}
		}
	})
	if len(problems) > 0 {
		return nil, fmt.Errorf("the change would add structural errors, so it wasn't proposed: %s", strings.Join(problems, "; "))
	}
	canonical, _ := json.Marshal(e) // stored field order, unknown fields already refused
	changes := Diff(current, canonical)
	if !isNew && len(changes.Added)+len(changes.Removed)+len(changes.Changed) == 0 {
		return nil, fmt.Errorf("%s is unchanged; nothing to propose", id)
	}
	p, err := tb.proposals.Add(Proposal{
		Kind: k, Element: canonical, Summary: summary, Sources: sources,
		New: isNew, Model: tb.model, Created: time.Now().UTC().Truncate(time.Second),
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"proposal": p.ID, "status": "pending approval in the web UI", "element": id, "changes": changes}
	if len(changes.Removed) > 0 {
		out["warning"] = fmt.Sprintf("the proposal removes %s; if that wasn't intended, propose again with the complete element", strings.Join(changes.Removed, ", "))
	}
	return out, nil
}

// runSPARQL sends a query to the repository's query endpoint. That endpoint
// only evaluates queries (updates go to a different one), so the tool is
// read-only by construction.
func (tb *Toolbox) runSPARQL(ctx context.Context, q string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", tb.sparql, strings.NewReader(url.Values{"query": {q}}.Encode()))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/sparql-results+json")
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, fmt.Errorf("graph store unreachable: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph store: %s: %s", resp.Status, strings.TrimSpace(string(body[:min(len(body), 500)])))
	}
	var res struct {
		Head    struct{ Vars []string } `json:"head"`
		Boolean *bool                   `json:"boolean"`
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, fmt.Errorf("unexpected answer from the graph store: %v", err)
	}
	if res.Boolean != nil {
		return map[string]bool{"answer": *res.Boolean}, nil
	}
	const maxRows = 200
	rows := []map[string]string{}
	for i, b := range res.Results.Bindings {
		if i == maxRows {
			break
		}
		row := map[string]string{}
		for k, v := range b {
			row[k] = v.Value
		}
		rows = append(rows, row)
	}
	out := map[string]any{"vars": res.Head.Vars, "rows": rows}
	if n := len(res.Results.Bindings); n > maxRows {
		out["truncated"] = fmt.Sprintf("%d of %d rows shown", maxRows, n)
	}
	return out, nil
}
