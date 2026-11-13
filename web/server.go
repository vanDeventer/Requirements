// Package web serves a browser UI for a requirements model directory: an
// overview of errors, gaps, evidence and coverage; lists per kind; search;
// element pages with links, evidence, trace and impact; and editing.
//
// Editing works on an element's JSON, the same form as its file. A save is
// checked against the whole model and refused if it adds structural errors.
// The server is for a few editors on one machine or a trusted network: it
// has no accounts, and writes are serialized by one lock.
package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

//go:embed templates static
var files embed.FS

// Options configure a Server.
type Options struct {
	Dir string // model directory
	Git bool   // commit every change to the git repository that holds Dir
}

// Server is the web UI. It implements http.Handler.
type Server struct {
	opt     Options
	mu      sync.RWMutex // guards m and the files in opt.Dir
	m       *req.Model
	pages   map[string]*template.Template
	handler http.Handler
}

// New loads the model and prepares the server.
func New(opt Options) (*Server, error) {
	m, err := req.Load(opt.Dir)
	if err != nil {
		return nil, err
	}
	if opt.Git {
		if out, err := exec.Command("git", "-C", opt.Dir, "rev-parse", "--is-inside-work-tree").CombinedOutput(); err != nil {
			return nil, fmt.Errorf("-git: %s is not in a git work tree: %s", opt.Dir, bytes.TrimSpace(out))
		}
	}
	s := &Server{opt: opt, m: m, pages: map[string]*template.Template{}}
	for _, p := range []string{"overview", "kind", "find", "element", "edit", "message"} {
		t, err := template.New("").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, err
		}
		s.pages[p] = t
	}

	mux := http.NewServeMux()
	static, _ := fs.Sub(files, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	mux.HandleFunc("GET /{$}", s.overview)
	mux.HandleFunc("GET /kind/{kind}", s.kind)
	mux.HandleFunc("GET /find", s.find)
	mux.HandleFunc("GET /e/{id}", s.element)
	mux.HandleFunc("GET /e/{id}/edit", s.editForm)
	mux.HandleFunc("POST /e/{id}/edit", s.editSave)
	mux.HandleFunc("POST /e/{id}/delete", s.delete)
	mux.HandleFunc("GET /new/{kind}", s.newForm)
	mux.HandleFunc("POST /new/{kind}", s.newSave)
	mux.HandleFunc("POST /reload", s.reload)
	// Browsers send Sec-Fetch-Site or Origin; this rejects cross-site POSTs,
	// so another web page can't make the browser edit the model.
	s.handler = http.NewCrossOriginProtection().Handler(mux)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

func (s *Server) render(w http.ResponseWriter, status int, page, title string, data any) {
	if d, ok := data.(editData); ok {
		d.Rows = min(max(strings.Count(d.JSON, "\n")+3, 16), 60)
		data = d
	}
	var buf bytes.Buffer
	err := s.pages[page].ExecuteTemplate(&buf, "layout", map[string]any{
		"Title": title,
		"Kinds": req.Kinds(),
		"Git":   s.opt.Git,
		"Dir":   s.opt.Dir,
		"Base":  filepath.Base(s.opt.Dir),
		"Data":  data,
	})
	if err != nil {
		log.Printf("render %s: %v", page, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) message(w http.ResponseWriter, status int, title, text string, details []string) {
	s.render(w, status, "message", title, map[string]any{"Title": title, "Text": text, "Details": details})
}

// --- read-only pages ---

type countRow struct {
	Kind req.Kind
	N    int
}

type originRow struct {
	ID      req.Ref
	Kind    req.Kind
	Name    string
	Covered bool
	Reasons []string
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.m
	counts := m.Count()
	var rows []countRow
	for _, k := range req.Kinds() {
		rows = append(rows, countRow{k, counts[k]})
	}
	var origins []originRow
	for _, e := range m.Elems() {
		switch e.(type) {
		case *req.Need, *req.Hazard, *req.ThreatScenario, *req.StandardClause:
		default:
			continue
		}
		if !req.Active(e) {
			continue
		}
		b := req.Base(e)
		c, err := m.Coverage(req.Ref(b.ID))
		if err != nil {
			continue
		}
		origins = append(origins, originRow{req.Ref(b.ID), req.KindOf(e), b.Name, c.Covered, c.Reasons})
	}
	unver, _ := m.Unverified("")
	covered := 0
	for _, o := range origins {
		if o.Covered {
			covered++
		}
	}
	s.render(w, http.StatusOK, "overview", "Overview", map[string]any{
		"Covered":    covered,
		"Counts":     rows,
		"Errors":     m.Validate(),
		"Gaps":       m.Gaps(),
		"Unverified": unver,
		"Origins":    origins,
	})
}

type listRow struct {
	ID      req.Ref
	Name    string
	Status  req.Ref
	Detail  string
	Verdict string // for requirements: verified, satisfied or open
}

func (s *Server) kind(w http.ResponseWriter, r *http.Request) {
	k := req.Kind(r.PathValue("kind"))
	if req.New(k) == nil {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no kind %q.", k), nil)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var rows []listRow
	for _, e := range s.m.Elems() {
		if req.KindOf(e) != k {
			continue
		}
		b := req.Base(e)
		row := listRow{ID: req.Ref(b.ID), Name: b.Name, Status: b.Status, Detail: detail(e)}
		if _, ok := e.(*req.Requirement); ok {
			ev, _ := s.m.Evidence(row.ID)
			row.Verdict = verdict(ev)
		}
		rows = append(rows, row)
	}
	s.render(w, http.StatusOK, "kind", string(k), map[string]any{"Kind": k, "Rows": rows, "IsReq": k == req.KindRequirement})
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	s.mu.RLock()
	defer s.mu.RUnlock()
	// An exact ID goes straight to the element.
	if _, ok := s.m.Get(req.Ref(q)); ok {
		http.Redirect(w, r, "/e/"+q, http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "find", "Search", map[string]any{"Q": q, "Hits": s.m.Find(q)})
}

func (s *Server) element(w http.ResponseWriter, r *http.Request) {
	id := req.Ref(r.PathValue("id"))
	s.mu.RLock()
	defer s.mu.RUnlock()
	m := s.m
	e, ok := m.Get(id)
	if !ok {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no element %s.", id), nil)
		return
	}
	b := req.Base(e)
	data := map[string]any{
		"ID":        id,
		"Kind":      req.KindOf(e),
		"E":         b,
		"Summary":   summary(e),
		"Detail":    detail(e),
		"Referrers": m.Referrers(id),
		"JSON":      linkedJSON(m, e),
		"Errors":    issuesFor(m.Validate(), id),
		"Gaps":      issuesFor(m.Gaps(), id),
	}
	if _, ok := e.(*req.Requirement); ok {
		ev, _ := m.Evidence(id)
		data["Evidence"] = ev
		data["Verdict"] = verdict(ev)
	}
	switch e.(type) {
	case *req.Need, *req.Hazard, *req.ThreatScenario, *req.StandardClause:
		c, _ := m.Coverage(id)
		data["Coverage"] = c
	}
	if t, err := m.Trace(id); err == nil {
		data["Trace"] = t
		data["SVG"] = traceSVG(m, t)
	}
	if im, err := m.Impact(id); err == nil {
		data["Impact"] = im
	}
	s.render(w, http.StatusOK, "element", string(id), data)
}

// --- editing ---

type editData struct {
	Kind     req.Kind
	ID       req.Ref // empty for a new element
	JSON     string
	Rows     int // textarea height
	Problems []string
	Fields   []fieldHelp
	Required []string
}

type fieldHelp struct {
	Field  string
	Refs   string   // allowed kinds, for reference fields
	Values []string // allowed concepts, for concept fields
	Scheme string
}

func (s *Server) help(k req.Kind) ([]fieldHelp, []string) {
	var fs []fieldHelp
	for _, d := range req.Describe(k) {
		h := fieldHelp{Field: d.Field, Scheme: d.Scheme}
		switch {
		case d.Scheme != "":
			if sc, ok := s.m.Scheme(d.Scheme); ok {
				for _, c := range sc.Concepts {
					h.Values = append(h.Values, c.ID)
				}
			}
		case d.Any:
			h.Refs = "any element"
		default:
			ks := make([]string, len(d.Refs))
			for i, k := range d.Refs {
				ks[i] = string(k)
			}
			h.Refs = strings.Join(ks, ", ")
		}
		fs = append(fs, h)
	}
	return fs, req.Required(k)
}

func (s *Server) editForm(w http.ResponseWriter, r *http.Request) {
	id := req.Ref(r.PathValue("id"))
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.m.Get(id)
	if !ok {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no element %s.", id), nil)
		return
	}
	k := req.KindOf(e)
	fields, required := s.help(k)
	s.render(w, http.StatusOK, "edit", "Edit "+string(id), editData{Kind: k, ID: id, JSON: pretty(e), Fields: fields, Required: required})
}

func (s *Server) newForm(w http.ResponseWriter, r *http.Request) {
	k := req.Kind(r.PathValue("kind"))
	if req.New(k) == nil {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no kind %q.", k), nil)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	fields, required := s.help(k)
	s.render(w, http.StatusOK, "edit", "New "+string(k), editData{Kind: k, JSON: skeleton(k), Fields: fields, Required: required})
}

func (s *Server) editSave(w http.ResponseWriter, r *http.Request) {
	id := req.Ref(r.PathValue("id"))
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m.Get(id)
	if !ok {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no element %s.", id), nil)
		return
	}
	s.save(w, r, req.KindOf(e), id)
}

func (s *Server) newSave(w http.ResponseWriter, r *http.Request) {
	k := req.Kind(r.PathValue("kind"))
	if req.New(k) == nil {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no kind %q.", k), nil)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.save(w, r, k, "")
}

// save applies the submitted JSON. The caller holds the write lock. An empty
// oldID means a new element.
func (s *Server) save(w http.ResponseWriter, r *http.Request, k req.Kind, oldID req.Ref) {
	text := r.FormValue("json")
	problems, id, gitErr := s.apply(k, oldID, []byte(text))
	if len(problems) > 0 {
		fields, required := s.help(k)
		title := "Edit " + string(oldID)
		if oldID == "" {
			title = "New " + string(k)
		}
		s.render(w, http.StatusUnprocessableEntity, "edit", title,
			editData{Kind: k, ID: oldID, JSON: text, Problems: problems, Fields: fields, Required: required})
		return
	}
	if gitErr != nil {
		s.message(w, http.StatusOK, "Saved, not committed", fmt.Sprintf("%s was saved, but the git commit failed.", id), []string{gitErr.Error()})
		return
	}
	http.Redirect(w, r, "/e/"+string(id), http.StatusSeeOther)
}

// apply decodes, checks, stores and commits one element. It returns the
// problems that prevented the save, or the saved ID and any git error.
func (s *Server) apply(k req.Kind, oldID req.Ref, data []byte) (problems []string, id req.Ref, gitErr error) {
	e, err := req.Decode(k, data)
	if err != nil {
		return []string{"The JSON doesn't decode: " + err.Error()}, "", nil
	}
	id = req.Ref(req.Base(e).ID)
	switch {
	case oldID != "" && id != oldID:
		return []string{fmt.Sprintf("The id can't change from %s to %s. Create a new element instead.", oldID, id)}, "", nil
	case !req.ValidID(string(id)):
		return []string{fmt.Sprintf("The id %q must match [A-Za-z][A-Za-z0-9-]*.", id)}, "", nil
	}
	prev, existed := s.m.Get(id)
	if oldID == "" && existed {
		return []string{fmt.Sprintf("%s already exists, as a %s.", id, req.KindOf(prev))}, "", nil
	}

	before := map[string]bool{}
	for _, i := range s.m.Validate() {
		before[i.String()] = true
	}
	s.m.Put(e)
	for _, i := range s.m.Validate() {
		if !before[i.String()] {
			problems = append(problems, i.String())
		}
	}
	undo := func() {
		if existed {
			s.m.Put(prev)
		} else {
			s.m.Remove(id)
		}
	}
	if len(problems) > 0 {
		undo()
		return problems, "", nil
	}
	if err := req.Save(s.opt.Dir, e); err != nil {
		undo()
		return []string{"Saving the file failed: " + err.Error()}, "", nil
	}
	verb := "Edit"
	if !existed {
		verb = "Add"
	}
	return nil, id, s.commit(k, id, fmt.Sprintf("%s %s %s", verb, id, req.Base(e).Name))
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id := req.Ref(r.PathValue("id"))
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m.Get(id)
	if !ok {
		s.message(w, http.StatusNotFound, "Not found", fmt.Sprintf("There is no element %s.", id), nil)
		return
	}
	if refs := s.m.Referrers(id); len(refs) > 0 {
		var ds []string
		for _, ref := range refs {
			ds = append(ds, fmt.Sprintf("%s.%s", ref.From, ref.Field))
		}
		s.message(w, http.StatusConflict, "Not deleted",
			fmt.Sprintf("%s is still referred to. Remove these references first, or set its status to obsolete to keep it for history.", id), ds)
		return
	}
	k := req.KindOf(e)
	if err := req.Delete(s.opt.Dir, k, id); err != nil {
		s.message(w, http.StatusInternalServerError, "Not deleted", err.Error(), nil)
		return
	}
	s.m.Remove(id)
	if err := s.commit(k, id, fmt.Sprintf("Delete %s %s", id, req.Base(e).Name)); err != nil {
		s.message(w, http.StatusOK, "Deleted, not committed", fmt.Sprintf("%s was deleted, but the git commit failed.", id), []string{err.Error()})
		return
	}
	http.Redirect(w, r, "/kind/"+string(k), http.StatusSeeOther)
}

// reload rereads the model from disk, e.g. after a git pull.
func (s *Server) reload(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := req.Load(s.opt.Dir)
	if err != nil {
		s.message(w, http.StatusUnprocessableEntity, "Not reloaded", "The files on disk don't load; the server keeps the model it had.", strings.Split(err.Error(), "\n"))
		return
	}
	s.m = m
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// commit records one element's file in git, if enabled.
func (s *Server) commit(k req.Kind, id req.Ref, msg string) error {
	if !s.opt.Git {
		return nil
	}
	path, ok := req.Path(k, id)
	if !ok {
		return fmt.Errorf("no path for %s", id)
	}
	for _, args := range [][]string{
		{"add", "-A", "--", path},
		{"commit", "-q", "-m", msg, "--", path},
	} {
		cmd := exec.Command("git", append([]string{"-C", s.opt.Dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %v: %s", args[0], err, bytes.TrimSpace(out))
		}
	}
	return nil
}

// --- helpers ---

func pretty(v any) string {
	b, _ := json.MarshalIndent(v, "", "\t")
	return string(b)
}

// skeleton is the starting JSON for a new element of kind k: the id, name,
// status and the kind's required fields.
func skeleton(k req.Kind) string {
	var b strings.Builder
	b.WriteString("{\n\t\"id\": \"\",\n\t\"name\": \"\",\n\t\"status\": \"proposed\"")
	t := reflect.TypeOf(req.New(k)).Elem()
	for _, f := range req.Required(k) {
		b.WriteString(",\n\t\"" + f + "\": " + zeroJSON(t, f))
	}
	b.WriteString("\n}")
	return b.String()
}

var timeType = reflect.TypeOf(time.Time{})

func zeroJSON(t reflect.Type, name string) string {
	for i := range t.NumField() {
		f := t.Field(i)
		if tag, _, _ := strings.Cut(f.Tag.Get("json"), ","); tag == name {
			switch {
			case f.Type == timeType:
				return `"` + time.Now().UTC().Truncate(time.Second).Format(time.RFC3339) + `"`
			case f.Type.Kind() == reflect.Slice:
				return "[]"
			case f.Type.Kind() == reflect.Bool:
				return "false"
			case f.Type.Kind() == reflect.Float64:
				return "0"
			}
			return `""`
		}
	}
	return `""`
}

func issuesFor(is []req.Issue, id req.Ref) []req.Issue {
	var out []req.Issue
	for _, i := range is {
		if i.ID == id {
			out = append(out, i)
		}
	}
	return out
}

func verdict(ev req.Evidence) string {
	switch {
	case ev.Verified:
		return "verified"
	case ev.Satisfied:
		return "satisfied"
	}
	return "open"
}

// summary is the main statement of an element, shown large on its page.
func summary(e req.Elem) string {
	switch e := e.(type) {
	case *req.Requirement:
		return e.Text
	case *req.Need:
		return e.Statement
	case *req.Idea:
		return e.Statement
	case *req.Assumption:
		return e.Text
	}
	return req.Base(e).Description
}

// detail is a one-line description of kind-specific attributes.
func detail(e req.Elem) string {
	var parts []string
	add := func(label string, v any) {
		if s := fmt.Sprint(v); s != "" && s != "[]" && s != "false" {
			parts = append(parts, label+" "+s)
		}
	}
	switch e := e.(type) {
	case *req.Requirement:
		add("on", e.Context)
		add("form", e.Form)
		add("concerns", strings.Trim(fmt.Sprint(e.Concerns), "[]"))
		if e.Safety != nil {
			add("ASIL", e.Safety.ASIL)
		}
		if e.Security != nil {
			add("CAL", e.Security.CAL)
		}
	case *req.Goal:
		add("pattern", e.Pattern)
		if e.Soft {
			parts = append(parts, "soft")
		}
		add("refinement", e.Refinement)
	case *req.DesignElement:
		add("kind", e.Kind)
		add("version", e.Version)
	case *req.Verification:
		add("", e.Result)
		add("by", e.Method)
		add("at", e.AtVersion)
	case *req.Satisfaction:
		add("by", e.By)
		add("at", e.AtVersion)
	case *req.StandardClause:
		add("", strings.TrimSpace(e.Standard+" "+e.Edition+" clause "+e.Clause))
	case *req.Assumption:
		add("kind", e.Kind)
	case *req.SolutionConcept:
		if e.Selected {
			parts = append(parts, "selected")
		}
	}
	if p := req.Base(e).Provenance; p != nil && p.GeneratedBy != "" {
		parts = append(parts, "AI-generated")
	}
	return strings.TrimSpace(strings.Join(parts, " · "))
}

var funcs = template.FuncMap{
	"kindclass": func(k req.Kind) string { return "k-" + strings.ToLower(string(k)) },
	"lower":     strings.ToLower,
}
