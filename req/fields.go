package req

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// FieldInfo describes one field of an element kind, for forms and for
// language models: its type, whether it is required, what a reference may
// point to, the allowed concept values, and a sentence of help.
type FieldInfo struct {
	Field     string      `json:"field"`
	Type      string      `json:"type"` // string, number, boolean, date-time, concept, element ID, object
	List      bool        `json:"list,omitempty"`
	Multiline bool        `json:"multiline,omitempty"`
	Required  bool        `json:"required,omitempty"`
	RefersTo  []Kind      `json:"refersTo,omitempty"` // allowed kinds of referenced elements; empty means any
	Values    []string    `json:"values,omitempty"`   // allowed concept values
	Help      string      `json:"help,omitempty"`
	Fields    []FieldInfo `json:"fields,omitempty"` // of a nested object
}

var timeType = reflect.TypeOf(time.Time{})

var multiline = []string{"description", "text", "statement", "rationale.text", "criteria.condition"}

// Fields describes the fields of kind k, in stored order, with the concept
// values from m's schemes.
func Fields(m *Model, k Kind) []FieldInfo {
	e := New(k)
	if e == nil {
		return nil
	}
	refs := map[string]FieldDoc{}
	for _, d := range Describe(k) {
		refs[d.Field] = d
	}
	required := Required(k)
	var walk func(t reflect.Type, prefix string) []FieldInfo
	walk = func(t reflect.Type, prefix string) []FieldInfo {
		var out []FieldInfo
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
			fi := FieldInfo{
				Field:     name,
				Required:  prefix == "" && slices.Contains(required, name),
				Multiline: slices.Contains(multiline, path),
				Help:      help(k, path),
			}
			ft := f.Type
			if ft.Kind() == reflect.Slice {
				fi.List, ft = true, ft.Elem()
			}
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch {
			case ft == timeType:
				fi.Type = "date-time"
			case ft.Kind() == reflect.Struct:
				fi.Type = "object"
				fi.Fields = walk(ft, path+".")
			case ft.Kind() == reflect.Bool:
				fi.Type = "boolean"
			case ft.Kind() == reflect.Float64:
				fi.Type = "number"
			default:
				fi.Type = "string"
			}
			if d, ok := refs[path]; ok {
				if d.Scheme != "" {
					fi.Type = "concept"
					if s, ok := m.Scheme(d.Scheme); ok {
						for _, c := range s.Concepts {
							fi.Values = append(fi.Values, c.ID)
						}
					}
				} else {
					fi.Type = "element ID"
					fi.RefersTo = d.Refs
				}
			}
			if path == "criteria.operator" {
				fi.Values = []string{"<", "<=", "=", ">=", ">"}
			}
			if k == KindGoal && path == "refinement" {
				fi.Values = []string{"AND", "OR"}
			}
			out = append(out, fi)
		}
		return out
	}
	return walk(reflect.TypeOf(e).Elem(), "")
}

// help returns the explanation of a field, preferring a kind-specific one.
func help(k Kind, path string) string {
	if h, ok := helpTexts[string(k)+"."+path]; ok {
		return h
	}
	return helpTexts[path]
}

var helpTexts = map[string]string{
	"id":          "Stable identifier: letters, digits and hyphens, starting with a letter. It can't change later.",
	"name":        "A short title.",
	"description": "A longer explanation, if the name and main text don't say enough.",
	"owner":       "The stakeholder responsible for it.",
	"status":      "Lifecycle: proposed, approved, rejected or obsolete. Rejected and obsolete elements stay for history but don't count.",
	"version":     "Version of this element, if you track one.",

	"rationale":              "Why it exists, or why it was chosen over alternatives.",
	"rationale.text":         "The reasoning, in a sentence or two.",
	"rationale.assumes":      "Assumptions the reasoning depends on.",
	"rationale.alternatives": "Elements that were considered and not chosen.",

	"provenance":              "Where the content came from. Filled in when a language-model proposal is approved; edit only to correct it.",
	"provenance.sources":      "Documents or URLs the content was taken from, one per line.",
	"provenance.generatedBy":  "The language model and version that generated it; empty when written by a person.",
	"provenance.approvedBy":   "The stakeholder who accepted the content.",
	"provenance.approvedOn":   "When the content was accepted (UTC).",
	"provenance.externalTool": "The tool it was imported from, e.g. reqif.",
	"provenance.externalID":   "Its ID in that tool; used when importing again.",

	"Stakeholder.role": "The stakeholder's role.",

	"Idea.statement":  "The idea in plain words, e.g. \"Update vehicle software over the air.\"",
	"Idea.proposedBy": "Who proposed it.",

	"Need.statement": "The stakeholder's problem or wish in their own words, without a solution.",
	"Need.heldBy":    "Stakeholders whose need it is.",
	"Need.origin":    "The ideas it came from.",

	"Goal.pattern":    "Achieve (reach a state), maintain (keep it), cease (stop it) or avoid (never reach it).",
	"Goal.soft":       "Tick for a goal without clear-cut satisfaction, such as \"usable\".",
	"Goal.refinement": "With subgoals: AND when all are needed, OR when they are alternatives.",
	"Goal.subgoals":   "Goals this goal is refined into.",
	"Goal.addresses":  "The needs, hazards, threat scenarios or standard clauses this goal answers.",
	"Goal.assumes":    "Assumptions needed for the subgoals to achieve this goal.",

	"SolutionConcept.kind":      "Operational or system concept.",
	"SolutionConcept.addresses": "The goals this concept is a way of reaching.",
	"SolutionConcept.selected":  "Tick for the chosen concept. Alternatives stay unticked.",

	"Requirement.text":            "The requirement itself, as one verifiable statement without design detail, e.g. \"The update manager shall …\".",
	"Requirement.context":         "What it is on: the product (vehicle or cloud software), an AI-assisted tool, or training data.",
	"Requirement.form":            "Functional (what it does), quality (how well), constraint (a limit) or interface.",
	"Requirement.concerns":        "Quality concerns it serves. Safety needs an ASIL below; security needs a CAL.",
	"Requirement.operationalizes": "The goals this requirement makes a component responsible for.",
	"Requirement.concept":         "The solution concept it is written within, if any.",
	"Requirement.children":        "Requirements it is split into; together they must imply this one.",
	"Requirement.refines":         "Requirements this one states more precisely, in the same scope.",
	"Requirement.derivedFrom":     "Design elements whose choice gave rise to this requirement.",
	"Requirement.assumes":         "Assumptions it relies on but doesn't control.",
	"Requirement.criteria":        "How to decide that it is met. Without a criterion it can't be verified.",
	"criteria.metric":             "What is measured. Ask an editor to add a metric to the metric scheme if it is missing.",
	"criteria.operator":           "How the measurement compares with the threshold.",
	"criteria.threshold":          "The value to compare with.",
	"criteria.unit":               "Unit symbol of the threshold, e.g. s, min, %, km/h. Empty for counts.",
	"criteria.condition":          "When or where the criterion applies, e.g. \"over 10 000 drive cycles\".",
	"Requirement.safety":          "For safety requirements (ISO 26262).",
	"safety.asil":                 "Automotive Safety Integrity Level, inherited from the safety goal.",
	"Requirement.security":        "For security requirements (ISO/SAE 21434).",
	"security.cal":                "Cybersecurity Assurance Level.",
	"security.threatScenario":     "The threat scenario it counters.",

	"Assumption.kind": "Domain property (a fact about the world) or expectation (about people's behaviour).",
	"Assumption.text": "What is assumed, e.g. \"The speed on the vehicle bus is correct to within 1 km/h.\"",

	"Hazard.source":         "Where the hazard is defined, e.g. a link into the HARA.",
	"ThreatScenario.source": "Where the threat scenario is defined, e.g. a link into the TARA.",

	"StandardClause.standard": "The standard or regulation, e.g. ISO 26262-3 or UNECE R156.",
	"StandardClause.edition":  "The edition, since clauses change between editions.",
	"StandardClause.clause":   "The clause number, e.g. 7.2.",

	"Specification.subject":  "The design element the specification is for.",
	"Specification.contains": "The requirements in the specification.",

	"DesignElement.kind":     "System, subsystem, component, interface, ML model or data set.",
	"DesignElement.parent":   "The design element it is part of.",
	"DesignElement.realizes": "The solution concept it embodies.",

	"Satisfaction.requirement": "The requirement claimed to be satisfied.",
	"Satisfaction.by":          "The design element claimed to satisfy it.",
	"Satisfaction.atVersion":   "The version of that design element the claim is about.",
	"Satisfaction.date":        "When the claim was made (UTC).",

	"Verification.verifies":  "The requirements this verification produces evidence for.",
	"Verification.item":      "The design element that was verified.",
	"Verification.method":    "Test (or a kind of test), analysis, inspection or demonstration.",
	"Verification.result":    "The verdict.",
	"Verification.evidence":  "Reports, logs or test runs, one per line, preferably as links.",
	"Verification.atVersion": "The version of the item that was verified. Must match the claim's version to count.",
	"Verification.date":      "When it was performed (UTC). The latest verification counts.",
}

var defaultPrefix = map[Kind]string{
	KindStakeholder: "SH-", KindIdea: "IDEA-", KindNeed: "NEED-", KindGoal: "G-",
	KindSolutionConcept: "SC-", KindRequirement: "REQ-", KindAssumption: "AS-",
	KindHazard: "HAZ-", KindThreatScenario: "TS-", KindStandardClause: "CL-",
	KindSpecification: "SPEC-", KindDesignElement: "DE-", KindSatisfaction: "SAT-",
	KindVerification: "VER-",
}

var numbered = regexp.MustCompile(`^(.*?)(\d+)$`)

// NextID suggests an unused ID for a new element of kind k: the most common
// numbered prefix among existing elements of that kind, with the next number
// and the same zero padding, e.g. REQ-008 after REQ-007.
func NextID(m *Model, k Kind) string {
	type series struct{ n, max, width int }
	byPrefix := map[string]*series{}
	best := ""
	for _, e := range m.Elems() {
		if KindOf(e) != k {
			continue
		}
		p := numbered.FindStringSubmatch(e.base().ID)
		if p == nil {
			continue
		}
		v, _ := strconv.Atoi(p[2])
		s := byPrefix[p[1]]
		if s == nil {
			s = &series{}
			byPrefix[p[1]] = s
		}
		s.n++
		s.max = max(s.max, v)
		s.width = max(s.width, len(p[2]))
		if best == "" || s.n > byPrefix[best].n || s.n == byPrefix[best].n && p[1] < best {
			best = p[1]
		}
	}
	prefix, next, width := defaultPrefix[k], 1, 3
	if s := byPrefix[best]; s != nil {
		prefix, next, width = best, s.max+1, s.width
	}
	for {
		id := fmt.Sprintf("%s%0*d", prefix, width, next)
		if !m.has(Ref(id)) {
			return id
		}
		next++
	}
}
