package exchange

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
)

// needsFile is the part of Sphinx-Needs' needs.json that Merge uses.
type needsFile struct {
	CurrentVersion string `json:"current_version"`
	Versions       map[string]struct {
		Needs map[string]need `json:"needs"`
	} `json:"versions"`
}

type need struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Description string   `json:"description"` // older Sphinx-Needs versions
	Type        string   `json:"type"`
	DocName     string   `json:"docname"`
	Links       []string `json:"links"`
	Refines     []string `json:"refines"` // a custom link type, if the project defines one
}

// ReadNeeds reads a Sphinx-Needs needs.json file. Needs whose type is in
// types become items (default: "req"). A "refines" link type becomes a
// refinement; the generic "links" are listed in Source.Skipped. Each
// document (docname) becomes a group.
func ReadNeeds(r io.Reader, location string, types []string) (*Source, error) {
	if len(types) == 0 {
		types = []string{"req"}
	}
	var f needsFile
	if err := json.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	version := f.CurrentVersion
	if _, ok := f.Versions[version]; !ok {
		var vs []string
		for v := range f.Versions {
			vs = append(vs, v)
		}
		if len(vs) == 0 {
			return nil, fmt.Errorf("%s: no versions", location)
		}
		sort.Strings(vs)
		version = vs[len(vs)-1]
	}
	needs := f.Versions[version].Needs
	ids := make([]string, 0, len(needs))
	for id := range needs {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	src := &Source{Tool: "sphinx-needs", Location: location}
	groups := map[string]*Group{}
	var docs []string
	links := 0
	for _, id := range ids {
		n := needs[id]
		if n.ID == "" {
			n.ID = id
		}
		if !slices.Contains(types, n.Type) {
			continue
		}
		text := strings.TrimSpace(n.Content)
		if text == "" {
			text = strings.TrimSpace(n.Description)
		}
		if text == "" {
			src.Skipped = append(src.Skipped, fmt.Sprintf("%s: no content", n.ID))
			continue
		}
		src.Items = append(src.Items, Item{ExternalID: n.ID, Name: n.Title, Text: text, Refines: n.Refines})
		links += len(n.Links)
		if n.DocName != "" {
			g, ok := groups[n.DocName]
			if !ok {
				g = &Group{ExternalID: n.DocName, Name: n.DocName}
				groups[n.DocName] = g
				docs = append(docs, n.DocName)
			}
			g.Items = append(g.Items, n.ID)
		}
	}
	if links > 0 {
		src.Skipped = append(src.Skipped, fmt.Sprintf("%d generic links not imported; their meaning isn't stated", links))
	}
	sort.Strings(docs)
	for _, d := range docs {
		src.Groups = append(src.Groups, *groups[d])
	}
	return src, nil
}
