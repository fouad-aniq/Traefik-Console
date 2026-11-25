package validation

import (
	fmt "fmt"
	strings "strings"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
)

var schemaHTTPRouters = map[string]bool{
	"entryPoints": true, "middlewares": true, "service": true, "rule": true,
	"parentRefs": true, "ruleSyntax": true, "priority": true, "tls": true, "observability": true,
}

var schemaHTTPServices = map[string]bool{
	"middlewares": true, "loadBalancer": true, "highestRandomWeight": true,
	"weighted": true, "mirroring": true, "failover": true,
}

var schemaTCPRouters = map[string]bool{
	"entryPoints": true, "middlewares": true, "service": true, "rule": true,
	"ruleSyntax": true, "priority": true, "tls": true,
}

var schemaTCPServices = map[string]bool{
	"loadBalancer": true, "weighted": true,
}

var schemaUDPRouters = map[string]bool{
	"entryPoints": true, "service": true,
}

var schemaUDPServices = map[string]bool{
	"loadBalancer": true, "weighted": true,
}

// ValidateDynamicAST validates the dynamic configuration AST for structural correctness,
// schema compliance, cross-resource reference integrity, protocol scoping, and middleware cycle detection.
func (cv *ConfigValidator) ValidateDynamicAST(doc *yamlv3.Node) Result {
	return cv.ValidateDynamicASTWithExternal(doc, nil)
}

// ValidateDynamicASTWithExternal validates the dynamic configuration AST while recognizing
// already-resolved external HTTP services so bare external references (e.g. service: backend)
// pass structural cross-reference validation without disabling validation or copying services.
func (cv *ConfigValidator) ValidateDynamicASTWithExternal(doc *yamlv3.Node, externalHTTPServices map[string]bool) Result {
	return cv.ValidateDynamicASTWithReferences(doc, externalHTTPServices, nil)
}

func (cv *ConfigValidator) ValidateDynamicASTWithReferences(doc *yamlv3.Node, externalHTTPServices, externalHTTPMiddlewares map[string]bool) Result {
	return cv.ValidateDynamicASTWithProtocolReferences(doc, ExternalReferences{HTTPServices: externalHTTPServices, HTTPMiddlewares: externalHTTPMiddlewares})
}

// ExternalReferences are the resources Traefik reports that the console does not own, by protocol,
// so a router may reference them (a TCP router may use a TCP service declared by Docker labels).
type ExternalReferences struct {
	HTTPServices, HTTPMiddlewares, TCPServices, TCPMiddlewares, UDPServices map[string]bool
}

func (cv *ConfigValidator) ValidateDynamicASTWithProtocolReferences(doc *yamlv3.Node, ext ExternalReferences) Result {
	externalHTTPServices, externalHTTPMiddlewares := ext.HTTPServices, ext.HTTPMiddlewares
	res := Result{
		Valid:    true,
		Errors:   make([]Issue, 0),
		Warnings: make([]Issue, 0),
	}

	if issues := validateGenerated(doc); len(issues) > 0 {
		res.Valid = false
		res.Errors = append(res.Errors, issues...)
	}
	root := yaml.GetRootMapping(doc)
	if root == nil {
		return res
	}

	httpServices := make(map[string]bool)
	tcpServices := make(map[string]bool)
	udpServices := make(map[string]bool)

	for ext := range externalHTTPServices {
		httpServices[ext] = true
		if !strings.Contains(ext, "@") {
			httpServices[fmt.Sprintf("%s@file", ext)] = true
		}
	}

	for _, pair := range []struct {
		from map[string]bool
		to   map[string]bool
	}{{ext.TCPServices, tcpServices}, {ext.UDPServices, udpServices}} {
		for name := range pair.from {
			pair.to[name] = true
			if !strings.Contains(name, "@") {
				pair.to[fmt.Sprintf("%s@file", name)] = true
			}
		}
	}

	httpMiddlewares := make(map[string]bool)
	for n := range externalHTTPMiddlewares {
		httpMiddlewares[n] = true
		if !strings.Contains(n, "@") {
			httpMiddlewares[n+"@file"] = true
		}
	}
	tcpMiddlewares := make(map[string]bool)
	for n := range ext.TCPMiddlewares {
		tcpMiddlewares[n] = true
		if !strings.Contains(n, "@") {
			tcpMiddlewares[n+"@file"] = true
		}
	}
	middlewareChains := make(map[string][]string)

	for _, proto := range []string{"http", "tcp", "udp"} {
		protoNode := yaml.GetMappingValue(root, proto)
		if protoNode == nil {
			continue
		}

		if servicesNode := yaml.GetMappingValue(protoNode, "services"); servicesNode != nil && servicesNode.Kind == yamlv3.MappingNode {
			for i := 0; i < len(servicesNode.Content); i += 2 {
				name := servicesNode.Content[i].Value
				switch proto {
				case "http":
					httpServices[name] = true
					httpServices[fmt.Sprintf("%s@file", name)] = true
				case "tcp":
					tcpServices[name] = true
					tcpServices[fmt.Sprintf("%s@file", name)] = true
				case "udp":
					udpServices[name] = true
					udpServices[fmt.Sprintf("%s@file", name)] = true
				}

				if i+1 < len(servicesNode.Content) {
					sVal := servicesNode.Content[i+1]
					cv.validateService(proto, name, sVal, &res)
				}
			}
		}

		if proto == "http" {
			if mwNode := yaml.GetMappingValue(protoNode, "middlewares"); mwNode != nil && mwNode.Kind == yamlv3.MappingNode {
				for i := 0; i < len(mwNode.Content); i += 2 {
					name := mwNode.Content[i].Value
					httpMiddlewares[name] = true
					httpMiddlewares[fmt.Sprintf("%s@file", name)] = true

					if i+1 < len(mwNode.Content) {
						mVal := mwNode.Content[i+1]
						if chainNode := yaml.GetMappingValue(mVal, "chain"); chainNode != nil {
							if chainList := yaml.GetMappingValue(chainNode, "middlewares"); chainList != nil && chainList.Kind == yamlv3.SequenceNode {
								var children []string
								for _, child := range chainList.Content {
									children = append(children, child.Value)
								}
								middlewareChains[name] = children
							}
						}
					}
				}
			}
		}

		if proto == "tcp" {
			if mwNode := yaml.GetMappingValue(protoNode, "middlewares"); mwNode != nil && mwNode.Kind == yamlv3.MappingNode {
				for i := 0; i < len(mwNode.Content); i += 2 {
					name := mwNode.Content[i].Value
					tcpMiddlewares[name] = true
					tcpMiddlewares[fmt.Sprintf("%s@file", name)] = true
				}
			}
		}
	}

	for _, proto := range []string{"http", "tcp", "udp"} {
		protoNode := yaml.GetMappingValue(root, proto)
		if protoNode == nil {
			continue
		}

		if routersNode := yaml.GetMappingValue(protoNode, "routers"); routersNode != nil && routersNode.Kind == yamlv3.MappingNode {
			for i := 0; i < len(routersNode.Content); i += 2 {
				rName := routersNode.Content[i].Value
				rPath := fmt.Sprintf("%s.routers.%s", proto, rName)

				if i+1 >= len(routersNode.Content) {
					res.Errors = append(res.Errors, Issue{
						Path:     rPath,
						Message:  "Empty router definition",
						RuleCode: "ROUTER_EMPTY",
					})
					continue
				}

				rVal := routersNode.Content[i+1]
				if rVal.Kind == yamlv3.MappingNode {
					for j := 0; j < len(rVal.Content); j += 2 {
						propName := rVal.Content[j].Value
						var allowed bool
						switch proto {
						case "http":
							allowed = schemaHTTPRouters[propName]
						case "tcp":
							allowed = schemaTCPRouters[propName]
						case "udp":
							allowed = schemaUDPRouters[propName]
						}
						if !allowed {
							res.Errors = append(res.Errors, Issue{
								Path:     fmt.Sprintf("%s.%s", rPath, propName),
								Message:  fmt.Sprintf("Unknown property '%s' on %s router schema (Traefik v3.7 file provider)", propName, proto),
								RuleCode: "SCHEMA_UNKNOWN_FIELD",
							})
						}
					}
				}
				ruleNode := yaml.GetMappingValue(rVal, "rule")
				if proto == "udp" {
					// UDP routers have no rule: they match every datagram on their entrypoints.
				} else if ruleNode == nil || strings.TrimSpace(ruleNode.Value) == "" {
					res.Errors = append(res.Errors, Issue{
						Path:     rPath + ".rule",
						Message:  "Router must specify a non-empty rule",
						RuleCode: "ROUTER_RULE_REQUIRED",
					})
				} else {
					ruleVal := strings.TrimSpace(ruleNode.Value)
					if proto == "http" && !strings.Contains(ruleVal, "(") {
						res.Warnings = append(res.Warnings, Issue{
							Path:     rPath + ".rule",
							Message:  fmt.Sprintf("Rule '%s' might lack valid matcher syntax like Host(`...`) or PathPrefix(`...`)", ruleVal),
							RuleCode: "ROUTER_RULE_SYNTAX_WARNING",
						})
					}
				}

				svcNode := yaml.GetMappingValue(rVal, "service")
				if svcNode == nil || strings.TrimSpace(svcNode.Value) == "" {
					res.Errors = append(res.Errors, Issue{
						Path:     rPath + ".service",
						Message:  "Router must specify a target service",
						RuleCode: "ROUTER_SERVICE_REQUIRED",
					})
				} else {
					svcName := strings.TrimSpace(svcNode.Value)
					// Check service reference within protocol scope
					var exists bool
					switch proto {
					case "http":
						exists = httpServices[svcName] || cv.knownInternalServices[svcName]
					case "tcp":
						exists = tcpServices[svcName] || (svcName == "noop@internal")
					case "udp":
						exists = udpServices[svcName]
					}

					if !exists {
						res.Errors = append(res.Errors, Issue{
							Path:     rPath + ".service",
							Message:  fmt.Sprintf("Referenced %s service '%s' does not exist in %s service pool", strings.ToUpper(proto), svcName, proto),
							RuleCode: "SERVICE_NOT_FOUND",
						})
					}
				}

				// HTTP and TCP middlewares are separate pools: a router only uses those of its own protocol.
				if pool := map[string]map[string]bool{"http": httpMiddlewares, "tcp": tcpMiddlewares}[proto]; pool != nil {
					if mwListNode := yaml.GetMappingValue(rVal, "middlewares"); mwListNode != nil && mwListNode.Kind == yamlv3.SequenceNode {
						for idx, mwElem := range mwListNode.Content {
							mwName := strings.TrimSpace(mwElem.Value)
							if !pool[mwName] {
								res.Errors = append(res.Errors, Issue{
									Path:     fmt.Sprintf("%s.middlewares[%d]", rPath, idx),
									Message:  fmt.Sprintf("Referenced %s middleware '%s' does not exist in configuration", strings.ToUpper(proto), mwName),
									RuleCode: "MIDDLEWARE_NOT_FOUND",
								})
							}
						}
					}
				}
			}
		}
	}

	// Native middleware fields and chain members are recursively validated.
	for _, proto := range []string{"http", "tcp"} {
		node := yaml.GetMappingValue(yaml.GetMappingValue(root, proto), "middlewares")
		if node == nil {
			continue
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			definition := node.Content[i+1]
			if definition.Kind != yamlv3.MappingNode || len(definition.Content) != 2 {
				res.Errors = append(res.Errors, Issue{Path: node.Content[i].Value, RuleCode: "SCHEMA_UNKNOWN_FIELD", Message: "one middleware type is required"})
				continue
			}
			typ := definition.Content[0].Value
			var spec map[string]any
			if definition.Content[1].Decode(&spec) != nil {
				res.Errors = append(res.Errors, Issue{Path: node.Content[i].Value, RuleCode: "SCHEMA_INVALID_TYPE", Message: "middleware must be an object"})
				continue
			}
			if proto == "http" && typ == "errors" {
				service, _ := spec["service"].(string)
				if !httpServices[service] && !cv.knownInternalServices[service] {
					res.Errors = append(res.Errors, Issue{Path: node.Content[i].Value, RuleCode: "SERVICE_NOT_FOUND", Message: "error middleware target service not found"})
				}
			}
			if proto == "http" && typ != "plugin" {
				if ValidateNativeMiddleware(typ, spec) != nil {
					res.Errors = append(res.Errors, Issue{Path: node.Content[i].Value, RuleCode: "SCHEMA_UNKNOWN_FIELD", Message: "invalid native middleware fields"})
				}
			}
			if proto == "tcp" {
				if ValidateNativeTCPMiddleware(typ, spec) != nil {
					res.Errors = append(res.Errors, Issue{Path: node.Content[i].Value, RuleCode: "SCHEMA_UNKNOWN_FIELD", Message: "invalid TCP middleware fields"})
				}
			}
		}
	}
	for name, members := range middlewareChains {
		for _, member := range members {
			if !httpMiddlewares[member] {
				res.Errors = append(res.Errors, Issue{Path: name, RuleCode: "MIDDLEWARE_NOT_FOUND", Message: "chain member not found"})
			}
		}
	}
	cycleErrors := cv.detectMiddlewareCycles(middlewareChains)
	res.Errors = append(res.Errors, cycleErrors...)

	if len(res.Errors) > 0 {
		res.Valid = false
	}

	return res
}

func (cv *ConfigValidator) validateService(proto, name string, sVal *yamlv3.Node, res *Result) {
	sPath := fmt.Sprintf("%s.services.%s", proto, name)
	if sVal == nil || sVal.Kind != yamlv3.MappingNode || len(sVal.Content) == 0 {
		res.Errors = append(res.Errors, Issue{
			Path:     sPath,
			Message:  "Empty service definition",
			RuleCode: "SERVICE_EMPTY",
		})
		return
	}

	for j := 0; j < len(sVal.Content); j += 2 {
		propName := sVal.Content[j].Value
		var allowed bool
		switch proto {
		case "http":
			allowed = schemaHTTPServices[propName]
		case "tcp":
			allowed = schemaTCPServices[propName]
		case "udp":
			allowed = schemaUDPServices[propName]
		}
		if !allowed {
			res.Errors = append(res.Errors, Issue{
				Path:     fmt.Sprintf("%s.%s", sPath, propName),
				Message:  fmt.Sprintf("Unknown property '%s' on %s service schema", propName, proto),
				RuleCode: "SCHEMA_UNKNOWN_FIELD",
			})
		}
	}

	// Verify loadBalancer has at least one server URL/address
	if lb := yaml.GetMappingValue(sVal, "loadBalancer"); lb != nil {
		servers := yaml.GetMappingValue(lb, "servers")
		if servers == nil || servers.Kind != yamlv3.SequenceNode || len(servers.Content) == 0 {
			res.Errors = append(res.Errors, Issue{
				Path:     sPath + ".loadBalancer.servers",
				Message:  "Service loadBalancer must define at least one backend server",
				RuleCode: "SERVICE_SERVERS_REQUIRED",
			})
		} else {
			for idx, srv := range servers.Content {
				urlNode := yaml.GetMappingValue(srv, "url")
				addrNode := yaml.GetMappingValue(srv, "address")
				if (urlNode == nil || strings.TrimSpace(urlNode.Value) == "") &&
					(addrNode == nil || strings.TrimSpace(addrNode.Value) == "") {
					res.Errors = append(res.Errors, Issue{
						Path:     fmt.Sprintf("%s.loadBalancer.servers[%d]", sPath, idx),
						Message:  "Server entry must specify a 'url' (HTTP) or 'address' (TCP/UDP)",
						RuleCode: "SERVER_ENDPOINT_REQUIRED",
					})
				}
			}
		}
	}
}
