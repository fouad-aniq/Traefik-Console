package yaml

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

const (
	DefaultMaxYAMLSize = 1024 * 1024 // 1 MiB per SECURITY.md
	DefaultMaxDepth    = 32          // Max recursion depth
	DefaultMaxAliases  = 100         // Bounded alias expansion
)

// ParserOptions BoundedParser options for YAML parsing.
type ParserOptions struct {
	MaxSize    int64
	MaxDepth   int
	MaxAliases int
}

func DefaultParserOptions() ParserOptions {
	return ParserOptions{
		MaxSize:    DefaultMaxYAMLSize,
		MaxDepth:   DefaultMaxDepth,
		MaxAliases: DefaultMaxAliases,
	}
}

// ParseBoundedYAML parses YAML input into a yaml.Node AST while enforcing size,
// recursion depth, duplicate key checks, and alias expansion limits.
func ParseBoundedYAML(r io.Reader, opts ParserOptions) (*yaml.Node, []byte, error) {
	if opts.MaxSize <= 0 {
		opts.MaxSize = DefaultMaxYAMLSize
	}
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = DefaultMaxDepth
	}
	if opts.MaxAliases <= 0 {
		opts.MaxAliases = DefaultMaxAliases
	}

	// 1. Read input with bounded limit (+1 byte to detect overflow)
	limited := io.LimitReader(r, opts.MaxSize+1)
	buf, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read YAML input: %w", err)
	}
	if int64(len(buf)) > opts.MaxSize {
		return nil, nil, ErrYAMLExceedsSizeLimit
	}

	// Handle empty input
	if len(bytes.TrimSpace(buf)) == 0 {
		doc := &yaml.Node{
			Kind: yaml.DocumentNode,
			Content: []*yaml.Node{
				{
					Kind: yaml.MappingNode,
				},
			},
		}
		return doc, buf, nil
	}

	// 2. Decode into yaml.Node AST
	var doc yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(buf))
	if err := decoder.Decode(&doc); err != nil {
		return nil, nil, fmt.Errorf("YAML syntax error: %w", err)
	}

	// Reject multi-document input: anything after the first "---" document
	// marker would bypass the structural validation below.
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, nil, fmt.Errorf("YAML syntax error in trailing document: %w", err)
		}
		return nil, nil, ErrYAMLMultipleDocuments
	}

	// 3. Validate structural boundaries (depth, duplicate keys, aliases)
	aliasCount := 0
	if err := validateNode(&doc, 0, opts.MaxDepth, &aliasCount, opts.MaxAliases); err != nil {
		return nil, nil, err
	}

	return &doc, buf, nil
}
