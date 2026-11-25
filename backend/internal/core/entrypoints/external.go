package entrypoints

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	yamlv3 "gopkg.in/yaml.v3"

	"github.com/traefik-console/internal/config/yaml"
	"github.com/traefik-console/internal/models"
)

// declared is one `entryPoints.<name>` of traefik.yml.
type declared struct {
	Name string
	Raw  map[string]any
}

// readDeclared lists the entrypoints of traefik.yml in file order. It only parses; no IO.
func readDeclared(rawStatic []byte) ([]declared, error) {
	if len(bytes.TrimSpace(rawStatic)) == 0 {
		return nil, nil
	}
	doc, _, err := yaml.ParseBoundedYAML(bytes.NewReader(rawStatic), yaml.DefaultParserOptions())
	if err != nil {
		return nil, err
	}
	node := yaml.GetMappingValue(yaml.GetRootMapping(doc), "entryPoints")
	if node == nil || node.Kind != yamlv3.MappingNode {
		return nil, nil
	}
	out := make([]declared, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		var raw map[string]any
		_ = node.Content[i+1].Decode(&raw)
		out = append(out, declared{Name: node.Content[i].Value, Raw: raw})
	}
	return out, nil
}

// reader walks a decoded YAML mapping and records every key it could not represent.
type reader struct{ unsupported []string }

func (r *reader) skip(path string) { r.unsupported = append(r.unsupported, path) }

// section returns the mapping at key (nil when absent) and whether the key is present at all.
// A present key with a null value is an empty section, which Traefik treats as "enabled".
func (r *reader) section(m map[string]any, path, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	delete(m, key)
	switch x := v.(type) {
	case nil:
		return map[string]any{}, true
	case map[string]any:
		return x, true
	}
	r.skip(path + key)
	return nil, false
}

func (r *reader) rest(m map[string]any, path string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, path+k)
	}
	sort.Strings(keys)
	r.unsupported = append(r.unsupported, keys...)
}

func (r *reader) boolean(m map[string]any, path, key string) (value, present bool) {
	v, ok := m[key]
	if !ok {
		return false, false
	}
	delete(m, key)
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		if b, err := strconv.ParseBool(x); err == nil {
			return b, true
		}
	}
	r.skip(path + key)
	return false, false
}

func (r *reader) text(m map[string]any, path, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	delete(m, key)
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	}
	r.skip(path + key)
	return ""
}

func (r *reader) number(m map[string]any, path, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	delete(m, key)
	switch x := v.(type) {
	case int:
		return x
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n
		}
	}
	r.skip(path + key)
	return 0
}

func (r *reader) list(m map[string]any, path, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	delete(m, key)
	items, isList := v.([]any)
	if v != nil && !isList {
		r.skip(path + key)
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, isText := item.(string)
		if !isText {
			r.skip(path + key)
			return nil
		}
		out = append(out, s)
	}
	return out
}

// splitAddress reads Traefik's `[host]:port[/tcp|/udp]`.
func splitAddress(address string) (host string, port int, protocol string, ok bool) {
	protocol = ProtocolTCP
	if base, suffix, found := strings.Cut(address, "/"); found {
		address, protocol = base, strings.ToLower(suffix)
		if protocol != ProtocolTCP && protocol != ProtocolUDP {
			return "", 0, "", false
		}
	}
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, "", false
	}
	port, err = strconv.Atoi(rawPort)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, "", false
	}
	return host, port, protocol, true
}

// fromTraefik converts a declared entrypoint to the console model. The second result lists the
// settings the model cannot hold; when it is not empty the conversion is lossy and the entrypoint
// must stay read-only.
func fromTraefik(name string, raw map[string]any) (models.EntryPointConfig, []string) {
	r := &reader{}
	m := cloneMap(raw)
	cfg := models.EntryPointConfig{Name: name, Protocol: ProtocolTCP}
	address := r.text(m, "", "address")
	cfg.Address = address
	if host, port, protocol, ok := splitAddress(address); ok {
		cfg.Host, cfg.Port, cfg.Protocol = host, port, protocol
	} else {
		r.skip("address")
		if strings.HasSuffix(strings.ToLower(address), "/udp") {
			cfg.Protocol = ProtocolUDP
		}
	}
	cfg.AsDefault, _ = r.boolean(m, "", "asDefault")
	cfg.ReusePort, _ = r.boolean(m, "", "reusePort")
	cfg.AllowACMEByPass, _ = r.boolean(m, "", "allowACMEByPass")

	if s, ok := r.section(m, "", "forwardedHeaders"); ok {
		f := &models.EntryPointForwardedHeaders{}
		f.Insecure, _ = r.boolean(s, "forwardedHeaders.", "insecure")
		f.NotAppendXForwardedFor, _ = r.boolean(s, "forwardedHeaders.", "notAppendXForwardedFor")
		f.TrustedIPs = r.list(s, "forwardedHeaders.", "trustedIPs")
		f.Connection = r.list(s, "forwardedHeaders.", "connection")
		r.rest(s, "forwardedHeaders.")
		cfg.ForwardedHeaders = f
	}
	if s, ok := r.section(m, "", "proxyProtocol"); ok {
		p := &models.EntryPointProxyProtocol{}
		p.Insecure, _ = r.boolean(s, "proxyProtocol.", "insecure")
		p.TrustedIPs = r.list(s, "proxyProtocol.", "trustedIPs")
		r.rest(s, "proxyProtocol.")
		if !p.Insecure && len(p.TrustedIPs) == 0 {
			// `proxyProtocol: {}` enables the protocol while trusting nobody: not representable.
			r.skip("proxyProtocol")
		}
		cfg.ProxyProtocol = p
	}
	if s, ok := r.section(m, "", "transport"); ok {
		t := &models.EntryPointTransport{}
		if rt, ok := r.section(s, "transport.", "respondingTimeouts"); ok {
			t.ReadTimeout = r.text(rt, "transport.respondingTimeouts.", "readTimeout")
			t.WriteTimeout = r.text(rt, "transport.respondingTimeouts.", "writeTimeout")
			t.IdleTimeout = r.text(rt, "transport.respondingTimeouts.", "idleTimeout")
			r.rest(rt, "transport.respondingTimeouts.")
		}
		if lc, ok := r.section(s, "transport.", "lifeCycle"); ok {
			t.GraceTimeout = r.text(lc, "transport.lifeCycle.", "graceTimeOut")
			t.RequestAcceptGraceTimeout = r.text(lc, "transport.lifeCycle.", "requestAcceptGraceTimeout")
			r.rest(lc, "transport.lifeCycle.")
		}
		t.KeepAliveMaxTime = r.text(s, "transport.", "keepAliveMaxTime")
		t.KeepAliveMaxRequests = r.number(s, "transport.", "keepAliveMaxRequests")
		r.rest(s, "transport.")
		cfg.Transport = t
	}
	if s, ok := r.section(m, "", "http"); ok {
		cfg.HTTP = r.http(s)
	}
	if s, ok := r.section(m, "", "http2"); ok {
		cfg.HTTP2 = &models.EntryPointHTTP2{
			MaxConcurrentStreams:      r.number(s, "http2.", "maxConcurrentStreams"),
			MaxDecoderHeaderTableSize: r.number(s, "http2.", "maxDecoderHeaderTableSize"),
			MaxEncoderHeaderTableSize: r.number(s, "http2.", "maxEncoderHeaderTableSize"),
		}
		r.rest(s, "http2.")
	}
	if s, ok := r.section(m, "", "http3"); ok {
		cfg.HTTP3 = &models.EntryPointHTTP3{AdvertisedPort: r.number(s, "http3.", "advertisedPort")}
		r.rest(s, "http3.")
	}
	if s, ok := r.section(m, "", "udp"); ok {
		cfg.UDP = &models.EntryPointUDP{Timeout: r.text(s, "udp.", "timeout")}
		r.rest(s, "udp.")
	}
	if s, ok := r.section(m, "", "observability"); ok {
		o := &models.EntryPointObservability{}
		for key, target := range map[string]*bool{"accessLogs": &o.DisableAccessLogs, "metrics": &o.DisableMetrics, "tracing": &o.DisableTracing} {
			if on, present := r.boolean(s, "observability.", key); present {
				*target = !on
			}
		}
		o.TraceVerbosity = r.text(s, "observability.", "traceVerbosity")
		r.rest(s, "observability.")
		cfg.Observability = o
	}
	r.rest(m, "")
	normalize(&cfg)
	cfg.Kind = kindOf(cfg)
	sort.Strings(r.unsupported)
	return cfg, r.unsupported
}

func (r *reader) http(s map[string]any) *models.EntryPointHTTP {
	h := &models.EntryPointHTTP{}
	if red, ok := r.section(s, "http.", "redirections"); ok {
		if ep, ok := r.section(red, "http.redirections.", "entryPoint"); ok {
			const path = "http.redirections.entryPoint."
			target := &models.EntryPointRedirect{To: r.text(ep, path, "to"), Scheme: r.text(ep, path, "scheme"), Priority: r.number(ep, path, "priority")}
			if permanent, present := r.boolean(ep, path, "permanent"); present {
				target.Temporary = !permanent
			}
			r.rest(ep, path)
			h.Redirect = target
		}
		r.rest(red, "http.redirections.")
	}
	h.Middlewares = r.list(s, "http.", "middlewares")
	if tls, ok := r.section(s, "http.", "tls"); ok {
		t := &models.EntryPointTLS{Options: r.text(tls, "http.tls.", "options"), CertResolver: r.text(tls, "http.tls.", "certResolver")}
		if raw, present := tls["domains"]; present {
			delete(tls, "domains")
			items, _ := raw.([]any)
			for _, item := range items {
				d, isMap := item.(map[string]any)
				if !isMap {
					r.skip("http.tls.domains")
					break
				}
				d = cloneMap(d)
				domain := models.EntryPointTLSDomain{Main: r.text(d, "http.tls.domains.", "main"), SANs: r.list(d, "http.tls.domains.", "sans")}
				r.rest(d, "http.tls.domains.")
				t.Domains = append(t.Domains, domain)
			}
		}
		r.rest(tls, "http.tls.")
		h.TLS = t
	}
	if enc, ok := r.section(s, "http.", "encodedCharacters"); ok {
		for name, option := range EncodedCharacters {
			if allowed, present := r.boolean(enc, "http.encodedCharacters.", option); present && !allowed {
				h.DeniedEncodedCharacters = append(h.DeniedEncodedCharacters, name)
			}
		}
		sort.Strings(h.DeniedEncodedCharacters)
		r.rest(enc, "http.encodedCharacters.")
	}
	h.EncodeQuerySemicolons, _ = r.boolean(s, "http.", "encodeQuerySemicolons")
	if sanitize, present := r.boolean(s, "http.", "sanitizePath"); present {
		h.DisableSanitizePath = !sanitize
	}
	h.MaxHeaderBytes = r.number(s, "http.", "maxHeaderBytes")
	h.AliasHeadersStrategy = r.text(s, "http.", "aliasHeadersStrategy")
	r.rest(s, "http.")
	return h
}

// cloneMap copies nested mappings too, so reading a section never alters the caller's value.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if nested, ok := v.(map[string]any); ok {
			v = cloneMap(nested)
		}
		out[k] = v
	}
	return out
}

// adoptBlocker explains why a declared entrypoint cannot be taken over ("" when it can).
func adoptBlocker(name string, unsupported []string) string {
	switch {
	case name == InternalName:
		return "Traefik reserves this entrypoint for its API and dashboard."
	case !nameRegex.MatchString(name):
		return "Its name is not a valid console resource name."
	case len(unsupported) > 0:
		return fmt.Sprintf("It uses settings the console does not manage: %s.", strings.Join(unsupported, ", "))
	}
	return ""
}
