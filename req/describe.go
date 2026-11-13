package req

import (
	"slices"
	"sort"
)

// Kinds returns every element kind, in the order the model loads them.
func Kinds() []Kind {
	ks := make([]Kind, len(kinds))
	for i, ki := range kinds {
		ks[i] = ki.kind
	}
	return ks
}

// New returns an empty element of kind k, or nil for an unknown kind.
func New(k Kind) Elem {
	if ki, ok := infoFor(k); ok {
		return ki.make()
	}
	return nil
}

// FieldDoc describes one field of a kind that refers to something else.
type FieldDoc struct {
	Field  string // JSON name, dotted for nested fields
	Refs   []Kind // allowed target kinds for references; nil with Scheme set
	Any    bool   // a reference to an element of any kind
	Scheme string // concept scheme for concept fields
}

// Describe lists the reference and concept fields of kind k, including the
// optional nested ones (rationale, provenance, safety, security, criteria).
func Describe(k Kind) []FieldDoc {
	e := New(k)
	if e == nil {
		return nil
	}
	b := e.base()
	b.Rationale = &Rationale{}
	b.Provenance = &Provenance{}
	if r, ok := e.(*Requirement); ok {
		r.Safety = &SafetyAttrs{}
		r.Security = &SecurityAttrs{}
		r.Criteria = []AcceptanceCriterion{{}}
	}
	var ds []FieldDoc
	for _, l := range links(e) {
		ds = append(ds, FieldDoc{Field: l.field, Refs: l.kinds, Any: l.kinds == nil})
	}
	for _, f := range conceptFields(e) {
		ds = append(ds, FieldDoc{Field: f.field, Scheme: f.scheme})
	}
	return ds
}

// Required lists the top-level JSON fields that kind k requires.
func Required(k Kind) []string {
	e := New(k)
	if e == nil {
		return nil
	}
	var top []string
	for _, f := range required(e) {
		if !slices.Contains(top, f) && !containsDot(f) {
			top = append(top, f)
		}
	}
	return top
}

func containsDot(s string) bool {
	for _, c := range s {
		if c == '.' {
			return true
		}
	}
	return false
}

// Reference is one stored reference to an element.
type Reference struct {
	From  Ref    `json:"from"`
	Field string `json:"field"`
}

// Referrers returns every element field that refers to id, side relations
// included, sorted by referring ID.
func (m *Model) Referrers(id Ref) []Reference {
	var rs []Reference
	for _, e := range m.Elems() {
		for _, l := range links(e) {
			if slices.Contains(l.to, id) {
				rs = append(rs, Reference{From: Ref(e.base().ID), Field: l.field})
			}
		}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].From < rs[j].From })
	return rs
}

// Schemes returns the concept schemes, sorted by ID.
func (m *Model) Schemes() []*Scheme {
	ss := make([]*Scheme, 0, len(m.schemes))
	for _, s := range m.schemes {
		ss = append(ss, s)
	}
	sort.Slice(ss, func(i, j int) bool { return ss[i].ID < ss[j].ID })
	return ss
}

// Count returns the number of elements of each kind.
func (m *Model) Count() map[Kind]int {
	c := map[Kind]int{}
	for _, e := range m.elems {
		c[KindOf(e)]++
	}
	return c
}

// Decode decodes a JSON element of kind k strictly, as Load does.
func Decode(k Kind, data []byte) (Elem, error) {
	e := New(k)
	if e == nil {
		return nil, errUnknownKind(k)
	}
	if err := decodeBytes(data, e); err != nil {
		return nil, err
	}
	return e, nil
}

// Path returns the file of element id of kind k, relative to the model
// directory.
func Path(k Kind, id Ref) (string, bool) {
	ki, ok := infoFor(k)
	if !ok || !ValidID(string(id)) {
		return "", false
	}
	return ki.dir + "/" + string(id) + ".json", true
}

// Clone returns a copy of the model that can be changed without affecting m.
// Elements are shared; Put replaces them rather than changing them, so this
// is safe for trying out a change.
func (m *Model) Clone() *Model {
	c := NewModel()
	for id, e := range m.elems {
		c.elems[id] = e
	}
	for id, s := range m.schemes {
		c.schemes[id] = s
	}
	return c
}
