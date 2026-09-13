package webui

import (
	"io/fs"
	"testing"
)

func TestDist_ContainsIndexHTML(t *testing.T) {
	dist, err := Dist()
	if err != nil {
		t.Fatalf("Dist: %v", err)
	}

	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		t.Fatalf("reading index.html from the embedded frontend: %v", err)
	}
	if len(data) == 0 {
		t.Fatalf("expected index.html to be non-empty")
	}
}
