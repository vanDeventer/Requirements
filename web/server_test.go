package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newServer serves a fresh copy of the example model.
func newServer(t *testing.T, git bool) (*Server, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "model")
	if err := os.CopyFS(dir, os.DirFS("../example/ota")); err != nil {
		t.Fatal(err)
	}
	if git {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git not installed")
		}
		for _, args := range [][]string{
			{"init", "-q"},
			{"config", "user.name", "Test"},
			{"config", "user.email", "test@example.org"},
			{"add", "-A"},
			{"commit", "-q", "-m", "start"},
		} {
			if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v %s", args, err, out)
			}
		}
	}
	s, err := New(Options{Dir: dir, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func get(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w.Code, w.Body.String()
}

func post(t *testing.T, s *Server, path string, form url.Values, hdr ...string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func mustContain(t *testing.T, where, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s lacks %q", where, w)
		}
	}
}

func TestPages(t *testing.T) {
	s, _ := newServer(t, false)
	for _, c := range []struct {
		path  string
		code  int
		wants []string
	}{
		{"/", 200, []string{"unverified requirements", `href="/e/CL-R156-7-2"`, "REQ-002 is not verified", "REQ-005"}},
		{"/kind/Requirement", 200, []string{`href="/e/REQ-001"`, "verified", "on training-data"}},
		{"/kind/Nope", 404, nil},
		{"/e/REQ-001", 200, []string{"<svg", `href="/e/G-002"`, "verified by VER-001", "Install only when parked", "If REQ-001 changes"}},
		{"/e/CL-R156-7-2", 200, []string{"not covered", "REQ-002 is not verified"}},
		{"/e/REQ-007", 200, []string{"AI-generated", `href="https://example.org/docs/perception-data-spec.pdf"`}},
		{"/e/NOPE", 404, nil},
		{"/find?q=parking+brake", 200, []string{`href="/e/REQ-001"`}},
		{"/e/REQ-001/edit", 200, []string{"<textarea", "&#34;id&#34;: &#34;REQ-001&#34;", "one of", "<code>product</code>"}},
		{"/new/Assumption", 200, []string{"&#34;text&#34;: &#34;&#34;"}},
		{"/static/style.css", 200, []string{"--bg"}},
	} {
		code, body := get(t, s, c.path)
		if code != c.code {
			t.Errorf("GET %s = %d, want %d", c.path, code, c.code)
			continue
		}
		mustContain(t, c.path, body, c.wants...)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/find?q=REQ-004", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/e/REQ-004" {
		t.Errorf("search for an ID: %d %s", w.Code, w.Header().Get("Location"))
	}
}

const assumption = `{
	"id": "AS-009",
	"name": "Network <script>alert(1)</script>",
	"kind": "domain-property",
	"text": "The cellular network is available at least once a day."
}`

func TestEditing(t *testing.T) {
	s, dir := newServer(t, false)
	file := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(dir, rel))
		return string(b)
	}

	// A valid edit is saved and shown.
	edited := `{"id": "AS-001", "name": "Speed signal is trustworthy", "kind": "domain-property", "text": "The vehicle speed on the bus is correct to within 1 km/h."}`
	w := post(t, s, "/e/AS-001/edit", url.Values{"json": {edited}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("valid edit = %d: %s", w.Code, w.Body)
	}
	mustContain(t, "AS-001 file", file("assumptions/AS-001.json"), "trustworthy")
	_, body := get(t, s, "/e/AS-001")
	mustContain(t, "AS-001 page", body, "trustworthy")

	// Refused edits leave the file alone and say why.
	before := file("requirements/REQ-001.json")
	for _, c := range []struct{ name, json, want string }{
		{"dangling reference", `{"id": "REQ-001", "text": "x", "context": "product", "operationalizes": ["G-404"]}`, "G-404 does not exist"},
		{"id change", `{"id": "REQ-999", "text": "x", "context": "product"}`, "can&#39;t change from REQ-001 to REQ-999"},
		{"unknown field", `{"id": "REQ-001", "txet": "x"}`, "unknown field"},
		{"trailing data", `{"id": "REQ-001", "text": "x", "context": "product"}}`, "unexpected data"},
		{"bad concept", `{"id": "REQ-001", "text": "x", "context": "vehicle"}`, "not in scheme"},
	} {
		w := post(t, s, "/e/REQ-001/edit", url.Values{"json": {c.json}})
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", c.name, w.Code)
		}
		mustContain(t, c.name, w.Body.String(), "Not saved", c.want)
	}
	if file("requirements/REQ-001.json") != before {
		t.Error("a refused edit changed REQ-001's file")
	}
	if _, body := get(t, s, "/e/REQ-001"); !strings.Contains(body, "verified by VER-001") {
		t.Error("a refused edit changed the model in memory")
	}

	// New elements: created once, escaped on the page.
	w = post(t, s, "/new/Assumption", url.Values{"json": {assumption}})
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/e/AS-009" {
		t.Fatalf("new = %d %s: %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	_, body = get(t, s, "/e/AS-009")
	if strings.Contains(body, "<script>alert") {
		t.Error("element name not escaped")
	}
	if w := post(t, s, "/new/Assumption", url.Values{"json": {assumption}}); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("duplicate new = %d", w.Code)
	}

	// Delete: refused while referred to, done otherwise.
	w = post(t, s, "/e/AS-001/delete", nil)
	if w.Code != http.StatusConflict {
		t.Errorf("delete referenced = %d", w.Code)
	}
	mustContain(t, "refused delete", w.Body.String(), "REQ-001.assumes")
	if w := post(t, s, "/e/AS-009/delete", nil); w.Code != http.StatusSeeOther {
		t.Errorf("delete = %d: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "assumptions", "AS-009.json")); !os.IsNotExist(err) {
		t.Error("AS-009 file still exists")
	}
}

func TestCrossSitePostRefused(t *testing.T) {
	s, dir := newServer(t, false)
	w := post(t, s, "/e/AS-001/delete", nil, "Sec-Fetch-Site", "cross-site")
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST = %d, want 403", w.Code)
	}
	w = post(t, s, "/new/Assumption", url.Values{"json": {assumption}}, "Origin", "https://evil.example")
	if w.Code != http.StatusForbidden {
		t.Errorf("foreign-origin POST = %d, want 403", w.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "assumptions", "AS-009.json")); !os.IsNotExist(err) {
		t.Error("a cross-site POST created a file")
	}
}

func TestReload(t *testing.T) {
	s, dir := newServer(t, false)
	p := filepath.Join(dir, "assumptions", "AS-002.json")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), "Driver parks when asked", "Driver parks soon", 1)), 0o644)
	if w := post(t, s, "/reload", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("reload = %d", w.Code)
	}
	_, body := get(t, s, "/e/AS-002")
	mustContain(t, "after reload", body, "Driver parks soon")

	os.WriteFile(p, []byte(`{"id": "AS-002", "bogus": 1}`), 0o644)
	if w := post(t, s, "/reload", nil); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("reload of a broken file = %d", w.Code)
	}
	_, body = get(t, s, "/e/AS-002")
	mustContain(t, "after failed reload", body, "Driver parks soon")
}

func TestGitCommits(t *testing.T) {
	s, dir := newServer(t, true)
	edited := `{"id": "AS-001", "name": "Speed signal checked", "kind": "domain-property", "text": "Speed is correct to within 1 km/h."}`
	if w := post(t, s, "/e/AS-001/edit", url.Values{"json": {edited}}); w.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d: %s", w.Code, w.Body)
	}
	if w := post(t, s, "/new/Assumption", url.Values{"json": {assumption}}); w.Code != http.StatusSeeOther {
		t.Fatalf("new = %d: %s", w.Code, w.Body)
	}
	if w := post(t, s, "/e/AS-009/delete", nil); w.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d: %s", w.Code, w.Body)
	}
	out, err := exec.Command("git", "-C", dir, "log", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	log := string(out)
	mustContain(t, "git log", log, "Edit AS-001 Speed signal checked", "Add AS-009", "Delete AS-009")
	if st, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); len(st) > 0 {
		t.Errorf("uncommitted changes left: %s", st)
	}
}
