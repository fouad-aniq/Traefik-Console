package yaml

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderYAMLDropsEmptyCollectionsOnly(t *testing.T) {
	src := "http:\n  routers: {}\n  middlewares: {}\n  services:\n    svc1:\n      loadBalancer:\n        servers:\n          - url: http://127.0.0.1:8080\n"
	doc := mustParse(t, src)

	out, err := RenderYAML(doc)
	if err != nil {
		t.Fatalf("RenderYAML() error = %v", err)
	}
	rendered := string(out)

	if strings.Contains(rendered, "routers") {
		t.Errorf("empty routers collection not dropped:\n%s", rendered)
	}
	if strings.Contains(rendered, "middlewares") {
		t.Errorf("empty middlewares collection not dropped:\n%s", rendered)
	}
	if !strings.Contains(rendered, "svc1") {
		t.Errorf("non-empty services collection must be kept:\n%s", rendered)
	}

	// The rendered output must itself remain valid single-document YAML.
	if _, _, err := ParseBoundedYAML(bytes.NewReader(out), DefaultParserOptions()); err != nil {
		t.Fatalf("rendered output no longer parses: %v\n%s", err, rendered)
	}
}

func TestRenderYAMLDoesNotMutateInput(t *testing.T) {
	src := "http:\n  routers: {}\n"
	doc := mustParse(t, src)

	if _, err := RenderYAML(doc); err != nil {
		t.Fatalf("RenderYAML() error = %v", err)
	}

	root := GetRootMapping(doc)
	if GetMappingValue(root, "http") == nil || GetMappingValue(GetMappingValue(root, "http"), "routers") == nil {
		t.Error("RenderYAML must not mutate the document it renders")
	}
}
