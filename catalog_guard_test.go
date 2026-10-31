package main

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The proxy must never read the catalog agent's proposals directory.
func TestProxyDoesNotReferenceCatalogProposals(t *testing.T) {
	scanned := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != "." && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		scanned++
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for line := 1; sc.Scan(); line++ {
			if strings.Contains(sc.Text(), "catalog-proposals") {
				t.Errorf("%s:%d references catalog-proposals; the proxy must not read proposals", path, line)
			}
		}
		return sc.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("no non-test .go files scanned; the guard checked nothing")
	}
}
