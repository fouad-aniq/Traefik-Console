package yaml

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mustParse(t *testing.T, src string) *yaml.Node {
	t.Helper()
	doc, _, err := ParseBoundedYAML(strings.NewReader(src), DefaultParserOptions())
	if err != nil {
		t.Fatalf("ParseBoundedYAML() error = %v", err)
	}
	return doc
}

func TestGetRootMapping(t *testing.T) {
	doc := mustParse(t, "a: 1\n")
	root := GetRootMapping(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		t.Fatal("document root mapping not found")
	}

	if GetRootMapping(root) != root {
		t.Error("bare MappingNode must be returned as its own root")
	}

	if GetRootMapping(nil) != nil {
		t.Error("nil node must return nil")
	}

	seqDoc := mustParse(t, "- a\n- b\n")
	if GetRootMapping(seqDoc) != nil {
		t.Error("sequence root must return nil")
	}
}

func TestGetMappingValue(t *testing.T) {
	doc := mustParse(t, "a: 1\nb:\n  c: 2\n")
	root := GetRootMapping(doc)

	if v := GetMappingValue(root, "a"); v == nil || v.Value != "1" {
		t.Errorf("GetMappingValue(a) = %v, want scalar 1", v)
	}
	nested := GetMappingValue(root, "b")
	if nested == nil || nested.Kind != yaml.MappingNode {
		t.Fatalf("GetMappingValue(b) did not return the nested mapping")
	}
	if v := GetMappingValue(nested, "c"); v == nil || v.Value != "2" {
		t.Errorf("GetMappingValue(b.c) = %v, want scalar 2", v)
	}

	if GetMappingValue(root, "missing") != nil {
		t.Error("missing key must return nil")
	}
	if GetMappingValue(nil, "a") != nil {
		t.Error("nil mapping must return nil")
	}
}

func TestSetNestedValueCreatesAndOverwrites(t *testing.T) {
	doc := mustParse(t, "http:\n  routers:\n")

	if err := SetNestedValue(doc, map[string]any{"rule": "Host(`x`)"}, "http", "routers", "r1"); err != nil {
		t.Fatalf("SetNestedValue(create) error = %v", err)
	}
	httpNode := GetMappingValue(GetRootMapping(doc), "http")
	routersNode := GetMappingValue(httpNode, "routers")
	r1 := GetMappingValue(routersNode, "r1")
	if r1 == nil {
		t.Fatal("nested value r1 not created")
	}
	if rule := GetMappingValue(r1, "rule"); rule == nil || rule.Value != "Host(`x`)" {
		t.Errorf("r1.rule = %v, want Host(`x`)", rule)
	}

	if err := SetNestedValue(doc, map[string]any{"rule": "Host(`y`)"}, "http", "routers", "r1"); err != nil {
		t.Fatalf("SetNestedValue(overwrite) error = %v", err)
	}
	if rule := GetMappingValue(GetMappingValue(routersNode, "r1"), "rule"); rule == nil || rule.Value != "Host(`y`)" {
		t.Errorf("r1.rule after overwrite = %v, want Host(`y`)", rule)
	}

	if err := SetNestedValue(doc, 1); err == nil {
		t.Error("empty path must return an error")
	}
}

func TestDeleteNestedKey(t *testing.T) {
	doc := mustParse(t, "http:\n  routers:\n    r1:\n      rule: x\n    r2:\n      rule: y\n")

	if !DeleteNestedKey(doc, "http", "routers", "r1") {
		t.Fatal("DeleteNestedKey(existing) = false, want true")
	}
	routers := GetMappingValue(GetMappingValue(GetRootMapping(doc), "http"), "routers")
	if GetMappingValue(routers, "r1") != nil || GetMappingValue(routers, "r2") == nil {
		t.Fatal("r1 must be gone while r2 survives")
	}

	if DeleteNestedKey(doc, "http", "routers", "r1") {
		t.Error("deleting a missing key must return false")
	}
	if DeleteNestedKey(doc, "missing", "routers", "r1") {
		t.Error("deleting through a missing path must return false")
	}
}
