// Package exchange imports requirements from other requirements tools.
//
// Readers turn a tool's export into a Source: ReadReqIF for ReqIF, the
// exchange format of DOORS, Polarion, Codebeamer and Jama, and ReadNeeds for
// Sphinx-Needs' needs.json. Merge then maps a Source onto a model. Imported
// elements remember the tool and their ID there (Element.Provenance), so a
// later import of the same source updates them instead of adding duplicates.
//
// Only what the tools state unambiguously is imported: requirement text and
// name, the documents that group them (as Specifications) and refinement
// links. Goals, contexts, concerns and other trace links are added in this
// model, where they can be checked.
package exchange

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

// Source is the content of one export file.
type Source struct {
	Tool     string  // "reqif" or "sphinx-needs"
	Location string  // where the export came from, recorded in provenance
	Items    []Item  // requirements
	Groups   []Group // documents or specifications that contain items
	Skipped  []string
}

// Item is one requirement as the tool states it.
type Item struct {
	ExternalID string
	Name       string
	Text       string
	Refines    []string // external IDs of the items this one refines
}

// Group is a document or specification in the tool.
type Group struct {
	ExternalID string
	Name       string
	Items      []string // external IDs, in document order
}

// Options control how a Source becomes model elements.
type Options struct {
	Prefix  string  // prepended to new IDs, e.g. "PLR-"
	Context req.Ref // context of new requirements: tool, product or training-data
}

// Result is what Merge changed. Apply it with Save.
type Result struct {
	Created []req.Elem
	Updated []req.Elem
	Notes   []string
}

// Merge maps src onto m and returns the elements to create or update; m
// itself is not changed. Existing elements are matched by tool and external
// ID. When an existing requirement's text changes, its status goes back to
// "proposed" and its approval is cleared, since what was approved is no
// longer what it says.
func Merge(m *req.Model, src *Source, opt Options) (*Result, error) {
	if opt.Context == "" {
		return nil, fmt.Errorf("a context is required: tool, product or training-data")
	}
	if opt.Prefix != "" && !req.ValidID(strings.TrimSuffix(opt.Prefix, "-")) {
		return nil, fmt.Errorf("prefix %q must start with a letter and contain only letters, digits and '-'", opt.Prefix)
	}
	res := &Result{Notes: slices.Clone(src.Skipped)}

	existing := map[string]req.Elem{} // external ID -> element from this tool
	taken := map[req.Ref]bool{}
	for _, e := range m.Elems() {
		b := req.Base(e)
		taken[req.Ref(b.ID)] = true
		if p := b.Provenance; p != nil && p.ExternalTool == src.Tool && p.ExternalID != "" {
			existing[p.ExternalID] = e
		}
	}
	newID := func(ext string) req.Ref {
		base := opt.Prefix + sanitize(ext)
		if !req.ValidID(base) {
			base = "X" + base
		}
		id := req.Ref(base)
		for n := 2; taken[id]; n++ {
			id = req.Ref(fmt.Sprintf("%s-%d", base, n))
		}
		taken[id] = true
		return id
	}
	prov := func(ext string) *req.Provenance {
		return &req.Provenance{Sources: []string{src.Location}, ExternalTool: src.Tool, ExternalID: ext}
	}

	ids := map[string]req.Ref{} // external ID -> model ID, for links
	reqs := map[string]*req.Requirement{}
	for _, it := range src.Items {
		if _, dup := ids[it.ExternalID]; dup {
			res.Notes = append(res.Notes, fmt.Sprintf("%s appears twice; kept the first", it.ExternalID))
			continue
		}
		if old, ok := existing[it.ExternalID]; ok {
			r, isReq := old.(*req.Requirement)
			if !isReq {
				res.Notes = append(res.Notes, fmt.Sprintf("%s is already imported as %s %s; skipped", it.ExternalID, req.KindOf(old), req.Base(old).ID))
				continue
			}
			ids[it.ExternalID] = req.Ref(r.ID)
			c := clone(r)
			changed := false
			if c.Name != it.Name {
				c.Name, changed = it.Name, true
			}
			if c.Text != it.Text {
				c.Text, changed = it.Text, true
				if c.Status != "proposed" || c.Provenance.ApprovedBy != "" {
					c.Status = "proposed"
					c.Provenance.ApprovedBy, c.Provenance.ApprovedOn = "", time.Time{}
					res.Notes = append(res.Notes, fmt.Sprintf("%s: text changed in %s; status reset to proposed", c.ID, src.Tool))
				}
			}
			if changed {
				res.Updated = append(res.Updated, c)
			}
			reqs[it.ExternalID] = c
			continue
		}
		r := &req.Requirement{
			Element: req.Element{ID: string(newID(it.ExternalID)), Name: it.Name, Status: "proposed", Provenance: prov(it.ExternalID)},
			Text:    it.Text,
			Context: opt.Context,
		}
		ids[it.ExternalID] = req.Ref(r.ID)
		reqs[it.ExternalID] = r
		res.Created = append(res.Created, r)
	}

	// Refinement links, added to what is already there.
	for _, it := range src.Items {
		r, ok := reqs[it.ExternalID]
		if !ok {
			continue
		}
		for _, ext := range it.Refines {
			to, ok := ids[ext]
			if !ok {
				res.Notes = append(res.Notes, fmt.Sprintf("%s refines %s, which is not in the export", it.ExternalID, ext))
				continue
			}
			if !slices.Contains(r.Refines, to) {
				r.Refines = append(r.Refines, to)
				if !slices.ContainsFunc(res.Created, func(e req.Elem) bool { return e == req.Elem(r) }) &&
					!slices.ContainsFunc(res.Updated, func(e req.Elem) bool { return e == req.Elem(r) }) {
					res.Updated = append(res.Updated, r)
				}
			}
		}
	}

	for _, g := range src.Groups {
		var contains []req.Ref
		for _, ext := range g.Items {
			if id, ok := ids[ext]; ok {
				contains = append(contains, id)
			}
		}
		if old, ok := existing[g.ExternalID]; ok {
			s, isSpec := old.(*req.Specification)
			if !isSpec {
				res.Notes = append(res.Notes, fmt.Sprintf("group %s is already imported as %s %s; skipped", g.ExternalID, req.KindOf(old), req.Base(old).ID))
				continue
			}
			if s.Name != g.Name || !slices.Equal(s.Contains, contains) {
				c := *s
				c.Name, c.Contains = g.Name, contains
				res.Updated = append(res.Updated, &c)
			}
			continue
		}
		res.Created = append(res.Created, &req.Specification{
			Element:  req.Element{ID: string(newID(g.ExternalID)), Name: g.Name, Status: "proposed", Provenance: prov(g.ExternalID)},
			Contains: contains,
		})
	}
	return res, nil
}

// Save writes the result's elements into the model directory.
func (r *Result) Save(dir string) error {
	for _, e := range append(slices.Clone(r.Created), r.Updated...) {
		if err := req.Save(dir, e); err != nil {
			return err
		}
	}
	return nil
}

// clone copies a requirement deeply enough that Merge can change the copy.
func clone(r *req.Requirement) *req.Requirement {
	c := *r
	c.Refines = slices.Clone(r.Refines)
	p := req.Provenance{}
	if r.Provenance != nil {
		p = *r.Provenance
	}
	c.Provenance = &p
	return &c
}

// sanitize replaces the characters of an external ID that a model ID can't
// hold (anything but ASCII letters, digits and '-') with '-'.
func sanitize(ext string) string {
	var b strings.Builder
	for _, c := range ext {
		if c < 128 && (c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			b.WriteRune(c)
		} else {
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
