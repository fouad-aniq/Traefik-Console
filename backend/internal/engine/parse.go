package engine

import (
	"bytes"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
)

type yamlNode = yamlv3.Node

func parsePair(before, after []byte) (*yamlNode, *yamlNode, error) {
	a, _, e := yaml.ParseBoundedYAML(bytes.NewReader(before), yaml.DefaultParserOptions())
	if e != nil {
		return nil, nil, e
	}
	b, _, e := yaml.ParseBoundedYAML(bytes.NewReader(after), yaml.DefaultParserOptions())
	return a, b, e
}
