package assist

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vanDeventer/Requirements/req"
)

// Proposal is a change suggested by a language model, waiting for a person.
type Proposal struct {
	ID      string          `json:"id"`
	Kind    req.Kind        `json:"kind"`
	Element json.RawMessage `json:"element"` // the complete element as proposed
	Summary string          `json:"summary"`
	Sources []string        `json:"sources,omitempty"`
	New     bool            `json:"new"`             // adds an element rather than changing one
	Model   string          `json:"model,omitempty"` // the language model that proposed it
	Created time.Time       `json:"created"`
}

// Proposals stores pending proposals as files in <model dir>/.proposals, so
// an MCP server in another process and the web UI see the same ones. The
// leading dot keeps them out of the model: Load ignores the directory.
type Proposals struct {
	dir string
	mu  sync.Mutex
}

// NewProposals returns the store for a model directory.
func NewProposals(modelDir string) *Proposals {
	return &Proposals{dir: filepath.Join(modelDir, ".proposals")}
}

// Add stores p under a new ID (P-1, P-2, ...) and returns it. Numbers are
// never reused, so commit messages such as "Approve P-3" stay unambiguous;
// the last number is kept in the file .last.
func (ps *Proposals) Add(p Proposal) (Proposal, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if err := os.MkdirAll(ps.dir, 0o755); err != nil {
		return p, err
	}
	n := 1
	last := filepath.Join(ps.dir, ".last")
	if b, err := os.ReadFile(last); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			n = v + 1
		}
	}
	entries, _ := os.ReadDir(ps.dir)
	for _, e := range entries {
		if v, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(e.Name(), "P-"), ".json")); err == nil && v >= n {
			n = v + 1
		}
	}
	// O_EXCL guards against another process taking the same number.
	for ; ; n++ {
		p.ID = fmt.Sprintf("P-%d", n)
		f, err := os.OpenFile(filepath.Join(ps.dir, p.ID+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return p, err
		}
		b, _ := req.Marshal(p)
		_, werr := f.Write(b)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.WriteFile(last, []byte(strconv.Itoa(n)+"\n"), 0o644)
		}
		return p, werr
	}
}

// List returns the pending proposals, oldest first.
func (ps *Proposals) List() ([]Proposal, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	entries, err := os.ReadDir(ps.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Proposal
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		p, err := ps.read(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(out[i].ID, "P-"))
		b, _ := strconv.Atoi(strings.TrimPrefix(out[j].ID, "P-"))
		return a < b
	})
	return out, nil
}

// Get returns one proposal.
func (ps *Proposals) Get(id string) (Proposal, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.read(id)
}

func (ps *Proposals) read(id string) (Proposal, error) {
	var p Proposal
	if !validProposalID(id) {
		return p, fmt.Errorf("no proposal %q", id)
	}
	b, err := os.ReadFile(filepath.Join(ps.dir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return p, fmt.Errorf("no proposal %q", id)
	}
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("proposal %s: %v", id, err)
	}
	return p, nil
}

// Remove deletes a proposal, after it was approved or rejected.
func (ps *Proposals) Remove(id string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if !validProposalID(id) {
		return fmt.Errorf("no proposal %q", id)
	}
	return os.Remove(filepath.Join(ps.dir, id+".json"))
}

func validProposalID(id string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(id, "P-"))
	return strings.HasPrefix(id, "P-") && err == nil && n > 0 && id == fmt.Sprintf("P-%d", n)
}

// Changes compares the top-level fields of the current and proposed element
// JSON. Removed fields matter most: a model that rewrites an element often
// drops fields it didn't mean to change.
type Changes struct {
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
	Changed []string `json:"changed,omitempty"`
}

// Diff returns the field changes from current to proposed. With no current
// element, every field is added.
func Diff(current, proposed []byte) Changes {
	var a, b map[string]json.RawMessage
	json.Unmarshal(current, &a)
	json.Unmarshal(proposed, &b)
	var c Changes
	for k, v := range b {
		old, ok := a[k]
		switch {
		case !ok:
			c.Added = append(c.Added, k)
		case !jsonEqual(old, v):
			c.Changed = append(c.Changed, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			c.Removed = append(c.Removed, k)
		}
	}
	sort.Strings(c.Added)
	sort.Strings(c.Removed)
	sort.Strings(c.Changed)
	return c
}

func jsonEqual(x, y json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(x, &a) != nil || json.Unmarshal(y, &b) != nil {
		return string(x) == string(y)
	}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}
