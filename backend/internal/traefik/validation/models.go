package validation

import (
	"fmt"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
)

type Issue struct {
	Path     string `json:"path"`
	Message  string `json:"message"`
	RuleCode string `json:"rule_code"`
}

type Result struct {
	Valid    bool    `json:"valid"`
	Errors   []Issue `json:"errors"`
	Warnings []Issue `json:"warnings"`
}

type ConfigValidator struct {
	knownInternalServices map[string]bool
}

func NewConfigValidator() *ConfigValidator {
	return &ConfigValidator{
		knownInternalServices: map[string]bool{
			"api@internal":        true,
			"dashboard@internal":  true,
			"noop@internal":       true,
			"acme-http@internal":  true,
			"prometheus@internal": true,
			"ping@internal":       true,
		},
	}
}

func validateGenerated(doc *yamlv3.Node) []Issue {
	var issues []Issue
	root := yaml.GetRootMapping(doc)
	if root == nil {
		return issues
	}
	allowedTopLevel := map[string]bool{
		"http": true, "tcp": true, "udp": true, "tls": true,
	}
	for i := 0; i < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if !allowedTopLevel[key] {
			issues = append(issues, Issue{
				Path:     key,
				Message:  fmt.Sprintf("Invalid top-level dynamic configuration section '%s'", key),
				RuleCode: "INVALID_TOP_LEVEL_KEY",
			})
		}
	}
	return issues
}
