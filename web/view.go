package web

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/vanDeventer/Requirements/req"
)

var jsonString = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)

// linkedJSON renders an element's JSON with keys marked, element IDs linked
// to their pages and http(s) URIs linked out. Everything else is escaped.
func linkedJSON(m *req.Model, e req.Elem) template.HTML {
	src := pretty(e)
	var b strings.Builder
	last := 0
	for _, loc := range jsonString.FindAllStringIndex(src, -1) {
		b.WriteString(html.EscapeString(src[last:loc[0]]))
		lit := src[loc[0]:loc[1]]
		last = loc[1]
		var val string
		_ = json.Unmarshal([]byte(lit), &val)
		_, isElem := m.Get(req.Ref(val))
		switch {
		case strings.HasPrefix(strings.TrimLeft(src[loc[1]:], " "), ":"):
			fmt.Fprintf(&b, `<span class="key">%s</span>`, html.EscapeString(lit))
		case isElem:
			fmt.Fprintf(&b, `<a href="/e/%s">%s</a>`, url.PathEscape(val), html.EscapeString(lit))
		case strings.HasPrefix(val, "https://") || strings.HasPrefix(val, "http://"):
			fmt.Fprintf(&b, `<a href="%s" rel="noopener noreferrer">%s</a>`, html.EscapeString(val), html.EscapeString(lit))
		default:
			fmt.Fprintf(&b, `<span class="str">%s</span>`, html.EscapeString(lit))
		}
	}
	b.WriteString(html.EscapeString(src[last:]))
	return template.HTML(b.String())
}

const (
	nodeW, nodeH = 156, 44
	gapX, gapY   = 14, 52
	pad          = 12
	maxNodes     = 150
)

// traceSVG draws a trace as layers: origins above the root, solution and
// evidence below, each node in the row of its distance from the root.
func traceSVG(m *req.Model, t req.TraceResult) template.HTML {
	if len(t.Depth) <= 1 {
		return ""
	}
	if len(t.Depth) > maxNodes {
		return template.HTML(fmt.Sprintf(`<p class="muted">The trace has %d elements, too many to draw; see the lists below.</p>`, len(t.Depth)))
	}
	rows := map[int][]req.Ref{}
	minD, maxD := 0, 0
	for id, d := range t.Depth {
		rows[d] = append(rows[d], id)
		minD, maxD = min(minD, d), max(maxD, d)
	}
	widest := 0
	for _, r := range rows {
		sort.Slice(r, func(i, j int) bool { return r[i] < r[j] })
		widest = max(widest, len(r))
	}
	orderRows(rows, minD, maxD, append(append([]req.Edge{}, t.Up...), t.Down...))
	width := 2*pad + widest*(nodeW+gapX) - gapX
	height := 2*pad + (maxD-minD+1)*(nodeH+gapY) - gapY

	type pos struct{ x, y int } // top-left
	at := map[req.Ref]pos{}
	for d, r := range rows {
		rw := len(r)*(nodeW+gapX) - gapX
		x0 := (width - rw) / 2
		for i, id := range r {
			at[id] = pos{x0 + i*(nodeW+gapX), pad + (d-minD)*(nodeH+gapY)}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="trace" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="Trace of %s">`,
		width, height, width, height, html.EscapeString(string(t.Root)))
	b.WriteString(`<defs><marker id="arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" class="arrowhead"/></marker></defs>`)

	seen := map[string]bool{}
	for _, e := range append(append([]req.Edge{}, t.Up...), t.Down...) {
		key := string(e.From) + "\x00" + e.Rel + "\x00" + string(e.To)
		if seen[key] {
			continue
		}
		seen[key] = true
		a, okA := at[e.From]
		c, okC := at[e.To]
		if !okA || !okC {
			continue
		}
		var x1, y1, x2, y2 int
		var d string
		switch {
		case a.y < c.y:
			x1, y1, x2, y2 = a.x+nodeW/2, a.y+nodeH, c.x+nodeW/2, c.y
			my := (y1 + y2) / 2
			d = fmt.Sprintf("M%d,%d C%d,%d %d,%d %d,%d", x1, y1, x1, my, x2, my, x2, y2)
		case a.y > c.y:
			x1, y1, x2, y2 = a.x+nodeW/2, a.y, c.x+nodeW/2, c.y+nodeH
			my := (y1 + y2) / 2
			d = fmt.Sprintf("M%d,%d C%d,%d %d,%d %d,%d", x1, y1, x1, my, x2, my, x2, y2)
		default: // same row: arc below
			x1, y1, x2, y2 = a.x+nodeW/2, a.y+nodeH, c.x+nodeW/2, c.y+nodeH
			d = fmt.Sprintf("M%d,%d C%d,%d %d,%d %d,%d", x1, y1, x1, y1+gapY-10, x2, y2+gapY-10, x2, y2)
		}
		label := fmt.Sprintf("%s %s %s", e.From, e.Rel, e.To)
		if e.Via != "" {
			label += " (" + string(e.Via) + ")"
		}
		fmt.Fprintf(&b, `<path d="%s" class="edge" marker-end="url(#arrow)"><title>%s</title></path>`, d, html.EscapeString(label))
	}

	for id, p := range at {
		e, _ := m.Get(id)
		k := req.KindOf(e)
		name := req.Base(e).Name
		if name == "" {
			name = detail(e)
		}
		cls := "node k-" + strings.ToLower(string(k))
		if id == t.Root {
			cls += " root"
		}
		if e != nil && !req.Active(e) {
			cls += " inactive"
		}
		fmt.Fprintf(&b, `<a href="/e/%s"><g class="%s"><title>%s %s: %s</title>`,
			url.PathEscape(string(id)), cls, k, html.EscapeString(string(id)), html.EscapeString(name))
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="6"/>`, p.x, p.y, nodeW, nodeH)
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="nid">%s</text>`, p.x+8, p.y+17, html.EscapeString(truncate(string(id), 20)))
		fmt.Fprintf(&b, `<text x="%d" y="%d" class="nname">%s</text>`, p.x+8, p.y+34, html.EscapeString(truncate(name, 23)))
		b.WriteString(`</g></a>`)
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

// orderRows reduces crossings: working outward from the root, each row is
// sorted by the mean position of its neighbours in the row nearer the root
// (the barycenter heuristic). Nodes without such neighbours keep their place.
func orderRows(rows map[int][]req.Ref, minD, maxD int, edges []req.Edge) {
	nbrs := map[req.Ref][]req.Ref{}
	for _, e := range edges {
		nbrs[e.From] = append(nbrs[e.From], e.To)
		nbrs[e.To] = append(nbrs[e.To], e.From)
	}
	place := func(d, toward int) {
		index := map[req.Ref]int{}
		for i, id := range rows[toward] {
			index[id] = i
		}
		r := rows[d]
		key := map[req.Ref]float64{}
		for i, id := range r {
			sum, n := 0, 0
			for _, o := range nbrs[id] {
				if j, ok := index[o]; ok {
					sum += j
					n++
				}
			}
			if n > 0 {
				key[id] = float64(sum) / float64(n)
			} else {
				key[id] = float64(i)
			}
		}
		sort.SliceStable(r, func(i, j int) bool { return key[r[i]] < key[r[j]] })
	}
	for d := -1; d >= minD; d-- {
		place(d, d+1)
	}
	for d := 1; d <= maxD; d++ {
		place(d, d-1)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
