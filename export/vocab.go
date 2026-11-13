package export

import _ "embed"

//go:embed vocab/req.ttl
var ontology []byte

// Ontology returns the req: vocabulary as OWL 2 RL in Turtle. Load it next
// to a Turtle export to let a reasoner infer trace shortcuts and types.
func Ontology() []byte { return ontology }
