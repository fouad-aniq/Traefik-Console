// Package yaml is the bounded YAML boundary for untrusted input: parsing,
// rendering, AST navigation, diffing, and normalization for Traefik static
// and dynamic configuration documents.
//
// It is a stateless library of pure functions over *yaml.Node, consumed
// directly by the engine, validation, status, plugins, and CLI layers.
// Unlike the config and core trees it deliberately has no coordinator:
// there is no state to construct, wire, or release — callers parse
// independent documents and always pass DefaultParserOptions().
//
// All parsing goes through ParseBoundedYAML, which enforces the input
// limits (payload size, nesting depth, duplicate keys, alias expansion,
// single document) before any document is handed to callers.
package yaml
