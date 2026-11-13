package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/vanDeventer/Requirements/assist"
	"github.com/vanDeventer/Requirements/req"
)

// --- conversations ---

const maxChats = 20

type chat struct {
	msgs    []assist.Message
	errs    map[int]string // error shown after message index
	updated time.Time
	busy    bool
}

type chats struct {
	mu sync.Mutex
	m  map[string]*chat
}

func newChats() *chats { return &chats{m: map[string]*chat{}} }

func (c *chats) get(id string) *chat {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[id]
}

func (c *chats) create() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= maxChats { // forget the least recently used
		var oldest string
		for id, ch := range c.m {
			if oldest == "" || ch.updated.Before(c.m[oldest].updated) {
				oldest = id
			}
		}
		delete(c.m, oldest)
	}
	b := make([]byte, 12)
	rand.Read(b)
	id := hex.EncodeToString(b)
	c.m[id] = &chat{errs: map[int]string{}, updated: time.Now()}
	return id
}

// --- chat page ---

type chatEntry struct {
	Role  string
	HTML  template.HTML
	Calls []callView
	Error string
}

type callView struct {
	Name    string
	Args    string
	Result  string
	IsError bool
}

func (s *Server) chatPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Enabled": s.opt.LLM != nil}
	if s.opt.LLM != nil {
		data["Model"] = s.opt.LLM.Model
		data["URL"] = s.opt.LLM.URL
	}
	id := r.FormValue("c")
	if ch := s.chats.get(id); ch != nil {
		data["ID"] = id
		s.chats.mu.Lock()
		msgs := slices.Clone(ch.msgs)
		errs := ch.errs
		s.chats.mu.Unlock()
		s.mu.RLock()
		data["Entries"] = entries(s.m, msgs, errs)
		s.mu.RUnlock()
	}
	s.render(w, http.StatusOK, "chat", "Chat", data)
}

func (s *Server) chatSend(w http.ResponseWriter, r *http.Request) {
	if s.opt.LLM == nil {
		s.message(w, http.StatusNotFound, "Chat is off", "Start the server with -llm-model to use a local model.", nil)
		return
	}
	text := strings.TrimSpace(r.FormValue("message"))
	id := r.FormValue("c")
	ch := s.chats.get(id)
	if ch == nil {
		id = s.chats.create()
		ch = s.chats.get(id)
	}
	if text == "" {
		http.Redirect(w, r, "/chat?c="+id, http.StatusSeeOther)
		return
	}
	s.chats.mu.Lock()
	if ch.busy {
		s.chats.mu.Unlock()
		s.message(w, http.StatusConflict, "Still answering", "The previous question in this conversation is still being answered.", nil)
		return
	}
	ch.busy = true
	history := slices.Clone(ch.msgs)
	s.chats.mu.Unlock()

	// No model lock is held here: each tool call takes its own read lock.
	added, err := s.opt.LLM.Reply(r.Context(), s.tools, history, text)

	s.chats.mu.Lock()
	ch.msgs = append(ch.msgs, added...)
	if err != nil {
		ch.errs[len(ch.msgs)-1] = err.Error()
	}
	ch.updated = time.Now()
	ch.busy = false
	s.chats.mu.Unlock()
	http.Redirect(w, r, "/chat?c="+id+"#end", http.StatusSeeOther)
}

// entries turns messages into what the page shows: user and assistant text,
// with each assistant turn's tool calls and their results.
func entries(m *req.Model, msgs []assist.Message, errs map[int]string) []chatEntry {
	results := map[string]assist.Message{}
	for _, msg := range msgs {
		if msg.Role == "tool" {
			results[msg.ToolCallID] = msg
		}
	}
	var out []chatEntry
	for i, msg := range msgs {
		var e chatEntry
		switch msg.Role {
		case "user":
			e = chatEntry{Role: "user", HTML: linkIDs(m, msg.Content)}
		case "assistant":
			e = chatEntry{Role: "assistant", HTML: linkIDs(m, msg.Content)}
			for _, c := range msg.ToolCalls {
				res := results[c.ID]
				e.Calls = append(e.Calls, callView{c.Function.Name, c.Function.Arguments, res.Content, res.IsError})
			}
		default:
			if errs[i] == "" {
				continue
			}
			e = chatEntry{Role: "assistant"}
		}
		e.Error = errs[i]
		out = append(out, e)
	}
	return out
}

var idToken = regexp.MustCompile(`[A-Za-z][A-Za-z0-9-]*[A-Za-z0-9]`)

// linkIDs escapes text and links every word that is an element ID.
func linkIDs(m *req.Model, text string) template.HTML {
	esc := html.EscapeString(text)
	return template.HTML(idToken.ReplaceAllStringFunc(esc, func(tok string) string {
		if _, ok := m.Get(req.Ref(tok)); ok {
			return `<a href="/e/` + tok + `">` + tok + `</a>`
		}
		return tok
	}))
}

// --- proposals ---

type proposalView struct {
	assist.Proposal
	ElemID   req.Ref
	Current  string // current JSON of the element, if it exists
	Proposed string
	Changes  assist.Changes
}

func (s *Server) proposalsPage(w http.ResponseWriter, r *http.Request) {
	ps, err := s.proposals.List()
	if err != nil {
		s.message(w, http.StatusInternalServerError, "Proposals unreadable", err.Error(), nil)
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var views []proposalView
	for _, p := range ps {
		v := proposalView{Proposal: p, Proposed: indent(p.Element)}
		if e, err := req.Decode(p.Kind, p.Element); err == nil {
			v.ElemID = req.Ref(req.Base(e).ID)
			v.Proposed = pretty(e) // stored field order, for comparing
			var current []byte
			if cur, ok := s.m.Get(v.ElemID); ok {
				v.Current = pretty(cur)
				current, _ = json.Marshal(cur)
			}
			proposed, _ := json.Marshal(e)
			v.Changes = assist.Diff(current, proposed)
		}
		views = append(views, v)
	}
	var stakeholders []req.Ref
	for _, sh := range req.All[req.Stakeholder](s.m) {
		stakeholders = append(stakeholders, req.Ref(sh.ID))
	}
	s.render(w, http.StatusOK, "proposals", "Proposals", map[string]any{"Proposals": views, "Stakeholders": stakeholders})
}

func indent(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.MarshalIndent(v, "", "\t")
	return string(b)
}

// approve applies a proposal: the element is saved as proposed, with
// provenance recording the language model, the approver and the time.
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("id")
	approver := req.Ref(r.FormValue("approver"))
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.proposals.Get(pid)
	if err != nil {
		s.message(w, http.StatusNotFound, "Not found", err.Error(), nil)
		return
	}
	if sh, ok := s.m.Get(approver); !ok || req.KindOf(sh) != req.KindStakeholder {
		s.message(w, http.StatusUnprocessableEntity, "Not approved", "Choose the stakeholder who approves the change.", nil)
		return
	}
	e, err := req.Decode(p.Kind, p.Element)
	if err != nil {
		s.message(w, http.StatusUnprocessableEntity, "Not approved", "The proposed element doesn't decode: "+err.Error(), nil)
		return
	}
	b := req.Base(e)
	prov := req.Provenance{}
	if old, ok := s.m.Get(req.Ref(b.ID)); ok && req.Base(old).Provenance != nil {
		prov = *req.Base(old).Provenance // keep import identity and sources
	}
	if b.Provenance != nil {
		prov.Sources = append(prov.Sources, b.Provenance.Sources...)
	}
	for _, src := range p.Sources {
		if !slices.Contains(prov.Sources, src) {
			prov.Sources = append(prov.Sources, src)
		}
	}
	prov.GeneratedBy = p.Model
	if prov.GeneratedBy == "" {
		prov.GeneratedBy = "language model"
	}
	prov.ApprovedBy = approver
	prov.ApprovedOn = time.Now().UTC().Truncate(time.Second)
	b.Provenance = &prov
	data, _ := json.Marshal(e)

	oldID := req.Ref("")
	if !p.New {
		oldID = req.Ref(b.ID)
	}
	msg := fmt.Sprintf("Approve %s: %s", p.ID, p.Summary)
	problems, id, gitErr := s.apply(p.Kind, oldID, data, msg)
	if len(problems) > 0 {
		s.message(w, http.StatusUnprocessableEntity, "Not approved",
			"The model changed since the proposal was made, and applying it now would add structural errors. Reject it, or edit the element by hand.", problems)
		return
	}
	if err := s.proposals.Remove(p.ID); err != nil {
		s.message(w, http.StatusOK, "Approved", fmt.Sprintf("%s was saved, but the proposal file couldn't be removed.", id), []string{err.Error()})
		return
	}
	if gitErr != nil {
		s.message(w, http.StatusOK, "Approved, not committed", fmt.Sprintf("%s was saved, but the git commit failed.", id), []string{gitErr.Error()})
		return
	}
	http.Redirect(w, r, "/e/"+string(id), http.StatusSeeOther)
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
	if err := s.proposals.Remove(r.PathValue("id")); err != nil {
		s.message(w, http.StatusNotFound, "Not found", err.Error(), nil)
		return
	}
	http.Redirect(w, r, "/proposals", http.StatusSeeOther)
}

// --- local access ---

// localOnly refuses requests whose Host header isn't a loopback name. A
// server on 127.0.0.1 is otherwise reachable from web pages through DNS
// rebinding, where Origin and Host both carry the attacker's name.
func localOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if hp, _, err := net.SplitHostPort(host); err == nil {
			host = hp
		}
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "this server only answers to localhost", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}
