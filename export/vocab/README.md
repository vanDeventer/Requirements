# vocab: the req: ontology

[req.ttl](req.ttl) defines the `req:` vocabulary used by the Turtle export:
its classes, properties, labels and comments, in OWL 2 RL so rule engines
such as GraphDB's owl2-rl rulesets can reason with it. It is compiled into
the binary and written by `req export ontology`.

What it lets a reasoner infer:

- inverses, such as `req:refinedBy` and `req:verifiedBy`;
- `req:tracesTo`, a transitive link along any trace path;
- `req:servesOrigin`, the goal path from a requirement or goal to the need,
  hazard, threat scenario or clause it serves, matching the tool's coverage;
- alignments with OSLC RM and PROV, such as `oslc_rm:satisfiedBy`.

Links between elements deliberately have no `rdfs:domain` or `rdfs:range`.
A reasoner would turn a link to the wrong kind of element into a false type
and hide the mistake from SHACL validation. The intended kinds are
documented with `schema:domainIncludes` and `schema:rangeIncludes` instead.

Edit this file by hand. The tests check that every term the exporter writes
is declared here.
