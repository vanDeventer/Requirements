# ota: over-the-air updates

A small model of over-the-air software updates. It is small enough to read
in full but touches safety, security, a regulation, design derivation, ML
models and AI-generated content, so every part of the tools has something
to show.

```
go run ./cmd/req -m example/ota check
go run ./cmd/req -m example/ota serve
```

## Layout

A model is a directory of JSON files, one per element, in one folder per
kind. The file name is the element's ID.

| Folder | Holds |
| --- | --- |
| `schemes/` | Allowed values for status, form, concern, ASIL, CAL, metrics and others |
| `stakeholders/`, `ideas/`, `needs/` | Who wants what, and why |
| `hazards/`, `threat-scenarios/`, `standard-clauses/` | Other origins of goals: from a HARA, a TARA, and UNECE R156 |
| `goals/`, `solution-concepts/` | What the system commits to, and the approaches considered |
| `requirements/`, `assumptions/`, `specifications/` | What must hold, what is relied on, and how requirements are grouped |
| `design-elements/` | The vehicle, update manager, signature verifier, an ML model and its training set |
| `satisfactions/`, `verifications/` | Claims that a design satisfies a requirement, and the evidence |

## What it shows

- A safety goal from a hazard (HAZ-001 → G-002 → REQ-001), verified.
- A cybersecurity goal from a threat scenario and a UNECE R156 clause
  (TS-001, CL-R156-7-2 → G-003 → REQ-002), whose latest test failed, so the
  clause is not covered.
- Refinement of a vague requirement into a measurable one (REQ-004 refines
  REQ-003).
- A requirement derived from a design choice (REQ-005 from the A/B
  partition design), with no acceptance criterion yet: a completeness gap.
- An ML model update as a safety issue (HAZ-002 → G-005 → REQ-006), with a
  training-data requirement (REQ-007) drafted by a language model and
  approved by a named person.
- A rejected alternative concept (SC-002) kept for the record.

The clause number of CL-R156-7-2 is illustrative; check it against the
published regulation before reusing it.
