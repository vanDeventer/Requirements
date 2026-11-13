package exchange

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// ReqIF (OMG Requirements Interchange Format 1.2) as exported by DOORS,
// Polarion, Codebeamer, Jama and others. Only the parts Merge needs are
// decoded. Attribute names follow the ReqIF Implementation Guide
// (ReqIF.Text, ReqIF.Name, ReqIF.ForeignID, ReqIF.ChapterName), with common
// tool-specific names as fallbacks.

type reqifDoc struct {
	Title   string `xml:"THE-HEADER>REQ-IF-HEADER>TITLE"`
	Content struct {
		Types struct {
			All []reqifType `xml:",any"`
		} `xml:"SPEC-TYPES"`
		Objects   []reqifObject   `xml:"SPEC-OBJECTS>SPEC-OBJECT"`
		Relations []reqifRelation `xml:"SPEC-RELATIONS>SPEC-RELATION"`
		Specs     []reqifSpec     `xml:"SPECIFICATIONS>SPECIFICATION"`
	} `xml:"CORE-CONTENT>REQ-IF-CONTENT"`
}

type reqifType struct {
	XMLName xml.Name
	ID      string `xml:"IDENTIFIER,attr"`
	Name    string `xml:"LONG-NAME,attr"`
	Attrs   struct {
		Defs []struct {
			ID   string `xml:"IDENTIFIER,attr"`
			Name string `xml:"LONG-NAME,attr"`
		} `xml:",any"`
	} `xml:"SPEC-ATTRIBUTES"`
}

type reqifObject struct {
	ID     string `xml:"IDENTIFIER,attr"`
	Name   string `xml:"LONG-NAME,attr"`
	Values struct {
		All []reqifValue `xml:",any"`
	} `xml:"VALUES"`
}

type reqifValue struct {
	XMLName xml.Name
	Attr    string `xml:"THE-VALUE,attr"` // string, integer, real, date, boolean values
	XHTML   struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"THE-VALUE"` // XHTML values
	Def struct {
		Refs []struct {
			ID string `xml:",chardata"`
		} `xml:",any"`
	} `xml:"DEFINITION"`
}

type reqifRelation struct {
	Type   string `xml:"TYPE>SPEC-RELATION-TYPE-REF"`
	Source string `xml:"SOURCE>SPEC-OBJECT-REF"`
	Target string `xml:"TARGET>SPEC-OBJECT-REF"`
}

type reqifSpec struct {
	ID       string           `xml:"IDENTIFIER,attr"`
	Name     string           `xml:"LONG-NAME,attr"`
	Children []reqifHierarchy `xml:"CHILDREN>SPEC-HIERARCHY"`
}

type reqifHierarchy struct {
	Object   string           `xml:"OBJECT>SPEC-OBJECT-REF"`
	Children []reqifHierarchy `xml:"CHILDREN>SPEC-HIERARCHY"`
}

var (
	textNames    = []string{"reqif.text", "object text", "description", "text"}
	nameNames    = []string{"reqif.name", "title", "name", "object heading"}
	foreignNames = []string{"reqif.foreignid", "foreign id", "id"}
	chapterNames = []string{"reqif.chaptername", "object heading", "heading"}
)

// ReadReqIF reads a .reqif file. A relation whose type name contains
// "refine" becomes a refinement, read as SOURCE refines TARGET; other
// relation types are listed in Source.Skipped.
func ReadReqIF(r io.Reader, location string) (*Source, error) {
	var doc reqifDoc
	d := xml.NewDecoder(r)
	d.CharsetReader = func(cs string, in io.Reader) (io.Reader, error) {
		if strings.EqualFold(cs, "utf-8") {
			return in, nil
		}
		return nil, fmt.Errorf("unsupported charset %q", cs)
	}
	if err := d.Decode(&doc); err != nil {
		return nil, fmt.Errorf("%s: %w", location, err)
	}
	src := &Source{Tool: "reqif", Location: location}

	defName := map[string]string{} // attribute definition ID -> lower-case long name
	relType := map[string]string{} // relation type ID -> long name
	for _, t := range doc.Content.Types.All {
		for _, a := range t.Attrs.Defs {
			defName[a.ID] = strings.ToLower(strings.TrimSpace(a.Name))
		}
		if t.XMLName.Local == "SPEC-RELATION-TYPE" {
			relType[t.ID] = t.Name
		}
	}

	ext := map[string]string{} // ReqIF IDENTIFIER -> external ID
	index := map[string]int{}  // ReqIF IDENTIFIER -> position in src.Items
	for _, o := range doc.Content.Objects {
		vals := map[string]string{}
		for _, v := range o.Values.All {
			if len(v.Def.Refs) == 0 {
				continue
			}
			name := defName[strings.TrimSpace(v.Def.Refs[0].ID)]
			text := v.Attr
			if strings.HasSuffix(v.XMLName.Local, "-XHTML") {
				text = xhtmlText(v.XHTML.Inner)
			}
			if name != "" && strings.TrimSpace(text) != "" {
				vals[name] = strings.TrimSpace(text)
			}
		}
		id := first(vals, foreignNames)
		if id == "" {
			id = o.ID
		}
		ext[o.ID] = id
		text := first(vals, textNames)
		if text == "" {
			if h := first(vals, chapterNames); h != "" {
				src.Skipped = append(src.Skipped, fmt.Sprintf("%s: heading %q, not a requirement", id, h))
			} else {
				src.Skipped = append(src.Skipped, fmt.Sprintf("%s: no text", id))
			}
			continue
		}
		name := first(vals, nameNames)
		if name == "" {
			name = o.Name
		}
		index[o.ID] = len(src.Items)
		src.Items = append(src.Items, Item{ExternalID: id, Name: name, Text: text})
	}

	other := map[string]int{}
	for _, rel := range doc.Content.Relations {
		name := relType[rel.Type]
		if !strings.Contains(strings.ToLower(name), "refine") {
			other[name]++
			continue
		}
		i, ok := index[rel.Source]
		if !ok {
			continue
		}
		if t, ok := ext[rel.Target]; ok {
			src.Items[i].Refines = append(src.Items[i].Refines, t)
		} else {
			src.Skipped = append(src.Skipped, fmt.Sprintf("%s refines unknown object %s", src.Items[i].ExternalID, rel.Target))
		}
	}
	names := make([]string, 0, len(other))
	for n := range other {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		src.Skipped = append(src.Skipped, fmt.Sprintf("%d relations of type %q not imported", other[n], n))
	}

	for _, s := range doc.Content.Specs {
		g := Group{ExternalID: s.ID, Name: s.Name}
		var walk func([]reqifHierarchy)
		walk = func(hs []reqifHierarchy) {
			for _, h := range hs {
				if _, ok := index[h.Object]; ok {
					g.Items = append(g.Items, ext[h.Object])
				}
				walk(h.Children)
			}
		}
		walk(s.Children)
		src.Groups = append(src.Groups, g)
	}
	return src, nil
}

// ReadReqIFZ reads the first .reqif file in a .reqifz archive.
func ReadReqIFZ(path string) (*Source, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	for _, f := range z.File {
		if strings.EqualFold(filepath.Ext(f.Name), ".reqif") {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return ReadReqIF(rc, path+"!"+f.Name)
		}
	}
	return nil, fmt.Errorf("%s: no .reqif file in the archive", path)
}

func first(vals map[string]string, names []string) string {
	for _, n := range names {
		if v := vals[n]; v != "" {
			return v
		}
	}
	return ""
}

// xhtmlText returns the text of an XHTML fragment, one line per block.
func xhtmlText(frag []byte) string {
	d := xml.NewDecoder(bytes.NewReader(frag))
	d.Strict = false
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			if t.Name.Local == "br" {
				b.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				b.WriteByte('\n')
			}
		}
	}
	var lines []string
	for _, l := range strings.Split(b.String(), "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}
