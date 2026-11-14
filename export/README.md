# export: Turtle, SysML v2, ontology and shapes

Writes the model for other tools. The Go model stays the source of truth;
everything here is generated, and a model with structural errors is
refused.

| File | Output | For |
| --- | --- | --- |
| [turtle.go](turtle.go) | RDF Turtle, with optional computed verdicts (`req:isVerified`, ...) | Graph stores, SPARQL, linking with other graphs |
| [sysml.go](sysml.go) | SysML v2 textual notation | Reading and analysis in SysML v2 tools (export-only) |
| [vocab.go](vocab.go), [vocab/](vocab/) | The `req:` ontology in OWL 2 RL | Reasoning: inverses, transitive trace links, coverage |
| [shapes.go](shapes.go) | SHACL shapes, generated from the model's concept schemes | Validating data in a graph store |

## How the pieces divide the work

- The **ontology** infers links, such as which requirements serve a hazard
  (`req:servesOrigin`). It never decides that something is missing; OWL
  assumes an open world.
- The **structural shapes** reject malformed data when installed in a store.
- The **gap shapes** report incompleteness on demand; installed, they would
  block every write, since stores reject warnings too.
- **Verdicts** (satisfied, verified, covered) come from the Go model and are
  exported as plain triples.

The tests include a small Turtle reader, so they check the actual triples.
The SysML output is checked for structure and resolvable names but not yet
by a SysML v2 parser.

Mapping details are in the vocabulary document's
[mapping section](../docs/requirements-model.md#mapping-to-turtle-and-sysml-v2).
