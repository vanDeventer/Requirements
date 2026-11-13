package req

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// File layout of a model directory:
//
//	<dir>/schemes/<scheme-id>.json
//	<dir>/<kind-dir>/<element-id>.json   e.g. requirements/REQ-042.json
//
// The kind of an element follows from its directory, so files carry no kind
// field. One file per element keeps git diffs and reviews small.

const schemeDir = "schemes"

// Load reads a model directory. Missing kind directories are fine. It returns
// every problem it finds, joined, rather than stopping at the first one.
func Load(dir string) (*Model, error) {
	m := NewModel()
	var errs []error
	where := map[Ref]string{} // ID -> file, to report duplicates

	err := eachJSON(filepath.Join(dir, schemeDir), func(path, name string) {
		s := new(Scheme)
		if err := decodeFile(path, s); err != nil {
			errs = append(errs, err)
			return
		}
		if s.ID != name {
			errs = append(errs, fmt.Errorf("%s: id %q does not match file name", path, s.ID))
			return
		}
		m.PutScheme(s)
	})
	if err != nil {
		errs = append(errs, err)
	}

	for _, ki := range kinds {
		err := eachJSON(filepath.Join(dir, ki.dir), func(path, name string) {
			e := ki.make()
			if err := decodeFile(path, e); err != nil {
				errs = append(errs, err)
				return
			}
			id := e.base().ID
			if id != name {
				errs = append(errs, fmt.Errorf("%s: id %q does not match file name", path, id))
				return
			}
			if prev, dup := where[Ref(id)]; dup {
				errs = append(errs, fmt.Errorf("%s: id %q already used by %s", path, id, prev))
				return
			}
			where[Ref(id)] = path
			m.Put(e)
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return m, errors.Join(errs...)
}

// eachJSON calls fn for every *.json file directly in dir, with the file name
// minus its extension. A missing dir is not an error.
func eachJSON(dir string, fn func(path, name string)) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, de := range entries {
		n := de.Name()
		if de.IsDir() || strings.HasPrefix(n, ".") || filepath.Ext(n) != ".json" {
			continue
		}
		fn(filepath.Join(dir, n), strings.TrimSuffix(n, ".json"))
	}
	return nil
}

// decodeFile decodes one JSON file strictly: unknown fields are errors, so a
// misspelled field name doesn't silently drop data.
func decodeFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Save writes e to its file in dir, creating the kind directory if needed.
// The write is atomic: a crash leaves either the old or the new file.
func Save(dir string, e Elem) error {
	ki, ok := infoFor(KindOf(e))
	if !ok {
		return fmt.Errorf("unknown element type %T", e)
	}
	id := e.base().ID
	if !ValidID(id) {
		return fmt.Errorf("invalid id %q", id)
	}
	return writeJSON(filepath.Join(dir, ki.dir, id+".json"), e)
}

// SaveScheme writes s to dir/schemes.
func SaveScheme(dir string, s *Scheme) error {
	if !ValidID(s.ID) {
		return fmt.Errorf("invalid scheme id %q", s.ID)
	}
	return writeJSON(filepath.Join(dir, schemeDir, s.ID+".json"), s)
}

// Delete removes the file of the element with the given kind and ID.
func Delete(dir string, k Kind, id Ref) error {
	ki, ok := infoFor(k)
	if !ok {
		return fmt.Errorf("unknown kind %q", k)
	}
	if !ValidID(string(id)) {
		return fmt.Errorf("invalid id %q", id)
	}
	return os.Remove(filepath.Join(dir, ki.dir, string(id)+".json"))
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
