# req: the requirements model

The core package. Everything else (import, export, web UI, language-model
tools) is built on it.

| File | What it holds |
| --- | --- |
| [model.go](model.go) | The element types (Stakeholder, Idea, Need, Goal, SolutionConcept, Requirement, Assumption, Hazard, ThreatScenario, StandardClause, Specification, DesignElement, Satisfaction, Verification), concept schemes, and the in-memory `Model` |
| [store.go](store.go) | Loading and saving a model directory: one JSON file per element, atomic writes, strict decoding |
| [validate.go](validate.go) | Structural validation: IDs, references and their kinds, concept values, required fields, cycles |
| [graph.go](graph.go) | Which fields are links, and in which direction a trace follows them (up toward origins, down toward the solution) |
| [query.go](query.go) | Queries: trace, impact, evidence, unverified, coverage, completeness gaps, text search |
| [describe.go](describe.go) | Helpers for the UI and tools: kind lists, field descriptions, referrers, cloning |

## Two kinds of checks

- `Validate` finds structural errors: a model with errors can't be exported.
- `Gaps` finds what is incomplete but valid, such as a hazard no goal
  addresses or a requirement without an acceptance criterion.

## Verdicts are computed

Whether a requirement is satisfied or verified, and whether a need, hazard,
threat scenario or clause is covered, is computed from satisfaction claims
and verifications, never stored. The rules are documented on the `Evidence`
type in [query.go](query.go). Every other part of the system, including the
language-model tools, takes its verdicts from here.

The vocabulary behind the types is explained in
[Requirements Vocabulary Semantics and Relationships.md](../Requirements%20Vocabulary%20Semantics%20and%20Relationships.md).
