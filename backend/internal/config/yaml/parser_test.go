package yaml

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestParseBoundedYAMLValidDocument(t *testing.T) {
	doc, raw, err := ParseBoundedYAML(strings.NewReader("http:\n  routers:\n    r1:\n      rule: Host(`a.example`)\n"), DefaultParserOptions())
	if err != nil {
		t.Fatalf("ParseBoundedYAML() error = %v", err)
	}
	root := GetRootMapping(doc)
	if root == nil {
		t.Fatal("root mapping is nil")
	}
	if GetMappingValue(root, "http") == nil {
		t.Error("http key missing from parsed document")
	}
	if len(raw) == 0 {
		t.Error("raw input not returned")
	}
}

func TestParseBoundedYAMLEmptyInputYieldsEmptyDocument(t *testing.T) {
	for _, in := range []string{"", "   \n\t\n"} {
		doc, _, err := ParseBoundedYAML(strings.NewReader(in), DefaultParserOptions())
		if err != nil {
			t.Fatalf("ParseBoundedYAML(%q) error = %v", in, err)
		}
		if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
			t.Errorf("ParseBoundedYAML(%q) did not return an empty mapping document", in)
		}
	}
}

func TestParseBoundedYAMLZeroOptionsFallBackToDefaults(t *testing.T) {
	if _, _, err := ParseBoundedYAML(strings.NewReader("a: 1\n"), ParserOptions{}); err != nil {
		t.Fatalf("zero-value options must fall back to defaults, got error: %v", err)
	}
}

func TestParseBoundedYAMLRejectsOversize(t *testing.T) {
	_, _, err := ParseBoundedYAML(strings.NewReader(strings.Repeat("a", 65)), ParserOptions{MaxSize: 64})
	if !errors.Is(err, ErrYAMLExceedsSizeLimit) {
		t.Errorf("custom limit error = %v, want ErrYAMLExceedsSizeLimit", err)
	}

	_, _, err = ParseBoundedYAML(strings.NewReader(strings.Repeat("a", int(DefaultMaxYAMLSize)+1)), DefaultParserOptions())
	if !errors.Is(err, ErrYAMLExceedsSizeLimit) {
		t.Errorf("default 1 MiB limit error = %v, want ErrYAMLExceedsSizeLimit", err)
	}
}

func nestedMappings(depth int) string {
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat("  ", i))
		b.WriteString("k" + strconv.Itoa(i) + ":\n")
	}
	b.WriteString(strings.Repeat("  ", depth))
	b.WriteString("leaf: 1\n")
	return b.String()
}

func TestParseBoundedYAMLRejectsExcessiveDepth(t *testing.T) {
	if _, _, err := ParseBoundedYAML(strings.NewReader(nestedMappings(8)), DefaultParserOptions()); err != nil {
		t.Fatalf("shallow document unexpectedly rejected: %v", err)
	}
	if _, _, err := ParseBoundedYAML(strings.NewReader(nestedMappings(64)), DefaultParserOptions()); !errors.Is(err, ErrYAMLExceedsDepth) {
		t.Fatalf("deep document error = %v, want ErrYAMLExceedsDepth", err)
	}
}

func TestParseBoundedYAMLRejectsDuplicateKeys(t *testing.T) {
	_, _, err := ParseBoundedYAML(strings.NewReader("a: 1\na: 2\n"), DefaultParserOptions())
	if !errors.Is(err, ErrYAMLDuplicateKey) {
		t.Fatalf("error = %v, want ErrYAMLDuplicateKey", err)
	}
}

func TestParseBoundedYAMLRejectsExcessiveAliases(t *testing.T) {
	var b strings.Builder
	b.WriteString("base: &base\n  x: 1\n")
	for i := 0; i < DefaultMaxAliases+1; i++ {
		b.WriteString("u" + strconv.Itoa(i) + ": *base\n")
	}
	_, _, err := ParseBoundedYAML(strings.NewReader(b.String()), DefaultParserOptions())
	if !errors.Is(err, ErrYAMLExcessiveAliases) {
		t.Fatalf("error = %v, want ErrYAMLExcessiveAliases", err)
	}
}

func TestParseBoundedYAMLRejectsMultipleDocuments(t *testing.T) {
	_, _, err := ParseBoundedYAML(strings.NewReader("a: 1\n---\nb: 2\n"), DefaultParserOptions())
	if !errors.Is(err, ErrYAMLMultipleDocuments) {
		t.Fatalf("error = %v, want ErrYAMLMultipleDocuments", err)
	}
}
