package assist

import (
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

// fieldDoc describes one field of an element kind for the language model.
type fieldDoc struct {
	Field    string     `json:"field"`
	Type     string     `json:"type"`
	Required bool       `json:"required,omitempty"`
	RefersTo []req.Kind `json:"refersTo,omitempty"` // allowed kinds of referenced elements
	Values   []string   `json:"values,omitempty"`   // allowed concept values
	Fields   []fieldDoc `json:"fields,omitempty"`   // of a nested object
}

var timeType = reflect.TypeOf(time.Time{})

// describe lists a kind's fields from its Go type, with the reference kinds
// and concept values the model allows, so a language model can write a
// valid element without guessing field names.
func describe(m *req.Model, k req.Kind) map[string]any {
	refs := map[string]req.FieldDoc{}
	for _, d := range req.Describe(k) {
		refs[d.Field] = d
	}
	required := req.Required(k)
	var walk func(t reflect.Type, prefix string) []fieldDoc
	walk = func(t reflect.Type, prefix string) []fieldDoc {
		var out []fieldDoc
		for i := range t.NumField() {
			f := t.Field(i)
			if f.Anonymous {
				out = append(out, walk(f.Type, prefix)...)
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			path := prefix + name
			fd := fieldDoc{Field: name, Required: prefix == "" && slices.Contains(required, name)}
			ft := f.Type
			list := false
			if ft.Kind() == reflect.Slice {
				list, ft = true, ft.Elem()
			}
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch {
			case ft == timeType:
				fd.Type = "date-time (RFC 3339, e.g. 2026-11-13T09:00:00Z)"
			case ft.Kind() == reflect.Struct:
				fd.Type = "object"
				fd.Fields = walk(ft, path+".")
			case ft.Kind() == reflect.Bool:
				fd.Type = "boolean"
			case ft.Kind() == reflect.Float64:
				fd.Type = "number"
			default:
				fd.Type = "string"
			}
			if d, ok := refs[path]; ok {
				switch {
				case d.Scheme != "":
					fd.Type = "concept"
					if s, ok := m.Scheme(d.Scheme); ok {
						for _, c := range s.Concepts {
							fd.Values = append(fd.Values, c.ID)
						}
					}
				case d.Any:
					fd.Type = "element ID"
				default:
					fd.Type = "element ID"
					fd.RefersTo = d.Refs
				}
			}
			if list {
				fd.Type = "list of " + fd.Type
			}
			out = append(out, fd)
		}
		return out
	}
	notes := []string{"Every element has an id matching [A-Za-z][A-Za-z0-9-]*, unique in the model."}
	if k == req.KindRequirement {
		notes = append(notes,
			"A requirement is verifiable only with criteria: each has a metric (a concept), an operator (<, <=, =, >=, >), a numeric threshold and optionally a unit and condition.",
			"safety needs the concern safety, and security needs the concern security.",
			"A metric that isn't in the list must first be added to the metric scheme by a person.")
	}
	return map[string]any{"kind": k, "fields": walk(reflect.TypeOf(req.New(k)).Elem(), ""), "notes": notes}
}

// fieldNames lists a kind's top-level JSON fields.
func fieldNames(k req.Kind) []string {
	if req.New(k) == nil {
		return nil
	}
	var names []string
	var walk func(t reflect.Type)
	walk = func(t reflect.Type) {
		for i := range t.NumField() {
			f := t.Field(i)
			if f.Anonymous {
				walk(f.Type)
				continue
			}
			if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
				names = append(names, name)
			}
		}
	}
	walk(reflect.TypeOf(req.New(k)).Elem())
	return names
}
