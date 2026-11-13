// Command req checks and queries a requirements model directory.
//
// Usage:
//
//	req [-m dir] [-json] command [argument]
//
// Commands:
//
//	check            structural errors, then completeness gaps
//	get ID           print one element
//	find WORDS       elements containing all the words
//	trace ID         what ID traces up to and down to
//	impact ID        what depends on ID, e.g. an assumption or a clause
//	evidence ID      whether requirement ID is satisfied and verified
//	unverified [ID]  unverified requirements, in specification ID or overall
//	coverage ID      whether a need, hazard, threat or clause is covered
//	gate [ID]        release gate: exit status 1 on structural errors or any
//	                 unverified requirement, in specification ID or overall
//	import FORMAT FILE
//	                 import requirements from reqif (.reqif or .reqifz) or
//	                 sphinx-needs (needs.json); needs -context
//	serve            web UI on -addr (default 127.0.0.1:8080); with -git,
//	                 every save is committed
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vanDeventer/Requirements/exchange"
	"github.com/vanDeventer/Requirements/req"
	"github.com/vanDeventer/Requirements/web"
)

func main() {
	dir := flag.String("m", ".", "model directory")
	asJSON := flag.Bool("json", false, "print results as JSON")
	context := flag.String("context", "", "import: context of new requirements (tool, product or training-data)")
	prefix := flag.String("prefix", "", "import: prefix for new IDs, e.g. PLR-")
	types := flag.String("types", "req", "import sphinx-needs: comma-separated need types to import")
	dryRun := flag.Bool("n", false, "import: show what would change without writing")
	addr := flag.String("addr", "127.0.0.1:8080", "serve: address to listen on")
	useGit := flag.Bool("git", false, "serve: commit every change to git")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: req [-m dir] [-json] check | get ID | find WORDS | trace ID | impact ID | evidence ID | unverified [SPEC] | coverage ID | gate [SPEC] | import reqif|sphinx-needs FILE | serve")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	m, err := req.Load(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	cmd, args := flag.Arg(0), flag.Args()[1:]
	arg := req.Ref(strings.Join(args, " "))
	needArg := func() {
		if arg == "" {
			fmt.Fprintf(os.Stderr, "req %s: missing argument\n", cmd)
			os.Exit(2)
		}
	}
	out := func(v any, text func()) {
		if *asJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if err := enc.Encode(v); err != nil {
				fail(err)
			}
			return
		}
		text()
	}

	switch cmd {
	case "check":
		errs, gaps := m.Validate(), m.Gaps()
		out(map[string]any{"errors": errs, "gaps": gaps}, func() {
			printIssues("errors", errs)
			printIssues("gaps", gaps)
		})
		if len(errs) > 0 {
			os.Exit(1)
		}
	case "get":
		needArg()
		e, ok := m.Get(arg)
		if !ok {
			fail(fmt.Errorf("%s does not exist", arg))
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(e); err != nil {
			fail(err)
		}
	case "find":
		needArg()
		hits := m.Find(string(arg))
		out(hits, func() {
			for _, h := range hits {
				fmt.Printf("%-14s %-16s %s\n", h.ID, h.Kind, h.Name)
			}
		})
	case "trace":
		needArg()
		t, err := m.Trace(arg)
		if err != nil {
			fail(err)
		}
		out(t, func() {
			fmt.Printf("up from %s:\n", t.Root)
			printEdges(t.Up)
			fmt.Printf("down from %s:\n", t.Root)
			printEdges(t.Down)
		})
	case "impact":
		needArg()
		r, err := m.Impact(arg)
		if err != nil {
			fail(err)
		}
		out(r, func() {
			fmt.Printf("depend on %s directly: %s\n", r.Changed, refs(r.Direct))
			fmt.Printf("affected below them:  %s\n", refs(r.Affected))
		})
	case "evidence":
		needArg()
		e, err := m.Evidence(arg)
		if err != nil {
			fail(err)
		}
		out(e, func() { printEvidence(e) })
	case "unverified":
		es, err := m.Unverified(arg)
		if err != nil {
			fail(err)
		}
		out(es, func() {
			for _, e := range es {
				printEvidence(e)
			}
		})
	case "coverage":
		needArg()
		c, err := m.Coverage(arg)
		if err != nil {
			fail(err)
		}
		out(c, func() {
			verdict := "covered"
			if !c.Covered {
				verdict = "NOT covered"
			}
			fmt.Printf("%s: %s\n", c.Origin, verdict)
			fmt.Printf("  goals: %s\n", refs(c.Goals))
			for _, r := range c.Reasons {
				fmt.Printf("  - %s\n", r)
			}
			for _, e := range c.Requirements {
				printEvidence(e)
			}
		})
	case "gate":
		errs := m.Validate()
		unver, err := m.Unverified(arg)
		if err != nil {
			fail(err)
		}
		gaps := m.Gaps()
		pass := len(errs) == 0 && len(unver) == 0
		out(map[string]any{"pass": pass, "errors": errs, "unverified": unver, "gaps": gaps}, func() {
			printIssues("errors", errs)
			fmt.Printf("unverified: %d\n", len(unver))
			for _, e := range unver {
				printEvidence(e)
			}
			printIssues("gaps (not blocking)", gaps)
			if pass {
				fmt.Println("gate: PASS")
			} else {
				fmt.Println("gate: FAIL")
			}
		})
		if !pass {
			os.Exit(1)
		}
	case "import":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: req [-m dir] -context C [-prefix P] [-n] import reqif|sphinx-needs FILE")
			os.Exit(2)
		}
		src, err := readSource(args[0], args[1], strings.Split(*types, ","))
		if err != nil {
			fail(err)
		}
		res, err := exchange.Merge(m, src, exchange.Options{Prefix: *prefix, Context: req.Ref(*context)})
		if err != nil {
			fail(err)
		}
		for _, e := range res.Created {
			m.Put(e)
		}
		for _, e := range res.Updated {
			m.Put(e)
		}
		errs := m.Validate()
		out(map[string]any{"created": res.Created, "updated": res.Updated, "notes": res.Notes, "errors": errs}, func() {
			for _, e := range res.Created {
				fmt.Printf("create %s %s\n", req.KindOf(e), req.Base(e).ID)
			}
			for _, e := range res.Updated {
				fmt.Printf("update %s %s\n", req.KindOf(e), req.Base(e).ID)
			}
			for _, n := range res.Notes {
				fmt.Printf("note: %s\n", n)
			}
			printIssues("errors after import", errs)
		})
		if len(errs) > 0 {
			fail(fmt.Errorf("not written: the imported model has structural errors"))
		}
		if *dryRun {
			return
		}
		if err := res.Save(*dir); err != nil {
			fail(err)
		}
	case "serve":
		srv, err := web.New(web.Options{Dir: *dir, Git: *useGit})
		if err != nil {
			fail(err)
		}
		hs := &http.Server{Addr: *addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second}
		fmt.Fprintf(os.Stderr, "serving %s on http://%s\n", *dir, *addr)
		fail(hs.ListenAndServe())
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func readSource(format, path string, types []string) (*exchange.Source, error) {
	switch format {
	case "reqif":
		if strings.EqualFold(filepath.Ext(path), ".reqifz") {
			return exchange.ReadReqIFZ(path)
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return exchange.ReadReqIF(f, path)
	case "sphinx-needs":
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return exchange.ReadNeeds(f, path, types)
	}
	return nil, fmt.Errorf("unknown format %q; want reqif or sphinx-needs", format)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func printIssues(title string, is []req.Issue) {
	fmt.Printf("%s: %d\n", title, len(is))
	for _, i := range is {
		fmt.Printf("  %s\n", i)
	}
}

func printEdges(es []req.Edge) {
	if len(es) == 0 {
		fmt.Println("  (nothing)")
	}
	for _, e := range es {
		via := ""
		if e.Via != "" {
			via = " (" + string(e.Via) + ")"
		}
		fmt.Printf("  %s --%s--> %s%s\n", e.From, e.Rel, e.To, via)
	}
}

func printEvidence(e req.Evidence) {
	mark := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	fmt.Printf("%s  satisfied: %s  verified: %s\n", e.Requirement, mark(e.Satisfied), mark(e.Verified))
	for _, r := range e.Reasons {
		fmt.Printf("    %s\n", r)
	}
}

func refs(rs []req.Ref) string {
	if len(rs) == 0 {
		return "(none)"
	}
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = string(r)
	}
	return strings.Join(s, ", ")
}
