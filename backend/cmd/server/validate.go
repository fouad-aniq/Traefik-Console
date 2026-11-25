package main

import (
	"io"
	"os"
	"strings"

	"github.com/traefik-console/internal/config/filesystem"
	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/traefik/validation"
)

// 1. Offline YAML Validator Command (`./server validate-config --file=dynamic.yml`)
func runValidateConfig() {
	var filePath string
	for i, arg := range os.Args {
		if after, ok := strings.CutPrefix(arg, "--file="); ok {
			filePath = after
		} else if arg == "--file" && i+1 < len(os.Args) {
			filePath = os.Args[i+1]
		}
	}
	if filePath == "" && len(os.Args) > 2 && !strings.HasPrefix(os.Args[2], "-") {
		filePath = os.Args[2]
	}
	if filePath == "" {
		diagLog.Errorf("usage: server validate-config --file=<path>")
		os.Exit(1)
	}

	// The validator reads an operator-named file, but only one that the
	// filesystem boundary confines to the working directory ('..' and
	// symlink escapes rejected), and only a bounded number of bytes.
	f, err := filesystem.OpenWithin(".", filePath)
	if err != nil {
		diagLog.Errorf("cannot open file %q: %v", filePath, err)
		os.Exit(1)
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)

	raw, err := io.ReadAll(io.LimitReader(f, yaml.DefaultMaxYAMLSize+1))
	if err != nil {
		diagLog.Errorf("error reading file %s: %v", filePath, err)
		os.Exit(1)
	}
	if int64(len(raw)) > yaml.DefaultMaxYAMLSize {
		diagLog.Errorf("file %s exceeds the maximum allowed size (%d bytes)", filePath, yaml.DefaultMaxYAMLSize)
		os.Exit(1)
	}

	doc, _, err := yaml.ParseBoundedYAML(strings.NewReader(string(raw)), yaml.DefaultParserOptions())
	if err != nil {
		diagLog.Errorf("YAML syntax error: %v", err)
		os.Exit(1)
	}

	validator := validation.NewConfigValidator()
	res := validator.ValidateDynamicAST(doc)
	if !res.Valid {
		diagLog.Errorf("configuration validation failed:")
		for _, e := range res.Errors {
			diagLog.Errorf(" - [%s] %s: %s", e.RuleCode, e.Path, e.Message)
		}
		os.Exit(1)
	}
	cliOut.Infof("configuration is valid")
}
