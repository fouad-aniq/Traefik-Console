package entrypoints

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/traefik-console/internal/engine"
	"github.com/traefik-console/internal/models"
)

const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"

	KindHTTP  = "http"
	KindHTTPS = "https"
	KindTCP   = "tcp"
	KindUDP   = "udp"
	// InternalName is the entrypoint Traefik reserves for its API, dashboard and ping.
	InternalName = "traefik"
)

// EncodedCharacters maps the API name of each encoded path character to its Traefik option.
var EncodedCharacters = map[string]string{
	"slash": "allowEncodedSlash", "backSlash": "allowEncodedBackSlash", "nullCharacter": "allowEncodedNullCharacter",
	"semicolon": "allowEncodedSemicolon", "percent": "allowEncodedPercent", "questionMark": "allowEncodedQuestionMark",
	"hash": "allowEncodedHash",
}

var (
	nameRegex       = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	hostnameRegex   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	domainRegex     = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	headerRegex     = regexp.MustCompile("^[A-Za-z0-9!#$%&'*+.^_`|~-]{1,128}$")
	middlewareRegex = regexp.MustCompile(`^[^\s@]{1,128}@[A-Za-z0-9_-]{1,64}$`)
	tlsOptionRegex  = regexp.MustCompile(`^[^\s@]{1,128}(@[A-Za-z0-9_-]{1,64})?$`)
	portOnlyRegex   = regexp.MustCompile(`^:(\d{1,5})$`)
)

// peer is another entrypoint of the same Traefik instance (managed or declared in traefik.yml).
type peer struct {
	Protocol string
	Host     string
	Port     int
	HTTP3    bool
}

// references is what an entrypoint may point at.
type references struct {
	Peers map[string]peer
	// Resolvers are the certificate resolvers that exist (managed or in traefik.yml).
	Resolvers map[string]bool
	// Middlewares are the managed HTTP middlewares and chains (file provider).
	Middlewares map[string]bool
}

type issues struct {
	name string
	list []models.ValidationError
}

func (i *issues) add(field, format string, args ...any) {
	i.list = append(i.list, models.ValidationError{ItemID: "entrypoint:" + i.name, Field: field, Message: fmt.Sprintf(format, args...)})
}

// build validates the input, drops everything that does not apply to its protocol or equals an
// empty section, and fills Address, Warnings and RenderedNode. It never touches the store or disk.
func build(in *models.EntryPointConfig, refs references) error {
	bad := &issues{name: in.Name}
	in.Warnings, in.RenderedNode, in.Address = nil, nil, ""
	in.Usage, in.Adoptable, in.AdoptBlocker = nil, false, ""
	in.Host = strings.TrimSpace(in.Host)
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if in.Protocol == "" {
		// The kind implies the protocol when the caller names only the kind.
		in.Protocol = ProtocolTCP
		if in.Kind == KindUDP {
			in.Protocol = ProtocolUDP
		}
	}
	if in.Protocol != ProtocolTCP && in.Protocol != ProtocolUDP {
		bad.add("protocol", "protocol must be tcp or udp")
		return &engine.ValidationFailure{Errors: bad.list}
	}
	switch in.Kind {
	case "", KindHTTP, KindHTTPS, KindTCP, KindUDP:
	default:
		bad.add("kind", "type must be http, https, tcp or udp")
		return &engine.ValidationFailure{Errors: bad.list}
	}
	// The type and the protocol must agree: only a UDP entrypoint listens on UDP.
	if in.Kind != "" && (in.Kind == KindUDP) != (in.Protocol == ProtocolUDP) {
		if in.Kind == KindUDP {
			bad.add("protocol", "a UDP entrypoint listens on UDP")
		} else {
			bad.add("protocol", "a %s entrypoint listens on TCP; choose the UDP type for a UDP port", strings.ToUpper(in.Kind))
		}
		return &engine.ValidationFailure{Errors: bad.list}
	}
	if in.Port < 1 || in.Port > 65535 {
		bad.add("port", "port must be between 1 and 65535")
	}
	if in.Host != "" && net.ParseIP(in.Host) == nil && !hostnameRegex.MatchString(in.Host) {
		bad.add("host", "host must be an IP address or a host name")
	}
	normalize(in)
	if in.Kind == "" {
		in.Kind = kindOf(*in)
	}
	checkKind(in, bad)

	if in.Protocol == ProtocolUDP {
		buildUDP(in, bad)
	} else {
		buildTCP(in, refs, bad)
	}
	checkPortConflicts(in, refs, bad)
	if len(bad.list) > 0 {
		slices.SortStableFunc(bad.list, func(a, b models.ValidationError) int { return strings.Compare(a.Field, b.Field) })
		return &engine.ValidationFailure{Errors: bad.list}
	}

	in.Address = formatAddress(in.Host, in.Port, in.Protocol)
	in.RenderedNode = render(*in)
	if in.ReusePort {
		in.Warnings = append(in.Warnings, "reusePort only works on Linux, FreeBSD, OpenBSD and macOS; it is ignored elsewhere.")
	}
	return nil
}

// kindOf works out the type of an entrypoint that does not state one (declared in traefik.yml, or
// stored before types existed). A TCP entrypoint without TLS may serve HTTP, so it is "http".
func kindOf(c models.EntryPointConfig) string {
	switch {
	case c.Protocol == ProtocolUDP:
		return KindUDP
	case c.HTTP != nil && c.HTTP.TLS != nil:
		return KindHTTPS
	}
	return KindHTTP
}

// checkKind refuses options that contradict the chosen type.
func checkKind(in *models.EntryPointConfig, bad *issues) {
	tls := in.HTTP != nil && in.HTTP.TLS != nil
	switch in.Kind {
	case KindHTTPS:
		if !tls {
			bad.add("http.tls", "a secure web (HTTPS) entrypoint needs TLS; choose the HTTP type for an entrypoint without it")
		}
	case KindHTTP:
		if tls {
			bad.add("http.tls", "TLS for every router makes this a secure web (HTTPS) entrypoint; choose that type")
		}
	case KindTCP:
		for field, set := range map[string]bool{
			"http": in.HTTP != nil, "http2": in.HTTP2 != nil, "http3": in.HTTP3 != nil,
			"forwarded_headers": in.ForwardedHeaders != nil, "allow_acme_bypass": in.AllowACMEByPass,
		} {
			if set {
				bad.add(field, "HTTP settings do not apply to a TCP service entrypoint; choose a web type to use them")
			}
		}
	}
}

// normalize removes empty sections so "nothing set" has one representation.
func normalize(in *models.EntryPointConfig) {
	if f := in.ForwardedHeaders; f != nil {
		f.TrustedIPs, f.Connection = clean(f.TrustedIPs), clean(f.Connection)
		if !f.Insecure && !f.NotAppendXForwardedFor && len(f.TrustedIPs) == 0 && len(f.Connection) == 0 {
			in.ForwardedHeaders = nil
		}
	}
	if p := in.ProxyProtocol; p != nil {
		p.TrustedIPs = clean(p.TrustedIPs)
		if !p.Insecure && len(p.TrustedIPs) == 0 {
			in.ProxyProtocol = nil
		}
	}
	if t := in.Transport; t != nil && *t == (models.EntryPointTransport{}) {
		in.Transport = nil
	}
	if h := in.HTTP; h != nil {
		h.Middlewares, h.DeniedEncodedCharacters = clean(h.Middlewares), clean(h.DeniedEncodedCharacters)
		if r := h.Redirect; r != nil {
			r.To, r.Scheme = strings.TrimSpace(r.To), strings.ToLower(strings.TrimSpace(r.Scheme))
			if r.Scheme == "https" {
				r.Scheme = ""
			}
		}
		if tls := h.TLS; tls != nil {
			tls.Options, tls.CertResolver = strings.TrimSpace(tls.Options), strings.TrimSpace(tls.CertResolver)
			for i := range tls.Domains {
				tls.Domains[i].Main, tls.Domains[i].SANs = strings.TrimSpace(tls.Domains[i].Main), clean(tls.Domains[i].SANs)
			}
		}
		if h.AliasHeadersStrategy == "keep" {
			h.AliasHeadersStrategy = ""
		}
		if h.Redirect == nil && h.TLS == nil && len(h.Middlewares) == 0 && len(h.DeniedEncodedCharacters) == 0 &&
			!h.EncodeQuerySemicolons && !h.DisableSanitizePath && h.MaxHeaderBytes == 0 && h.AliasHeadersStrategy == "" {
			in.HTTP = nil
		}
	}
	if h := in.HTTP2; h != nil && *h == (models.EntryPointHTTP2{}) {
		in.HTTP2 = nil
	}
	if u := in.UDP; u != nil {
		if u.Timeout = strings.TrimSpace(u.Timeout); u.Timeout == "" {
			in.UDP = nil
		}
	}
	if o := in.Observability; o != nil {
		if o.TraceVerbosity == "minimal" {
			o.TraceVerbosity = ""
		}
		if *o == (models.EntryPointObservability{}) {
			in.Observability = nil
		}
	}
}

func clean(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// buildUDP: a UDP entrypoint only has an address, reusePort and a session timeout.
func buildUDP(in *models.EntryPointConfig, bad *issues) {
	for field, set := range map[string]bool{
		"as_default": in.AsDefault, "allow_acme_bypass": in.AllowACMEByPass, "forwarded_headers": in.ForwardedHeaders != nil,
		"proxy_protocol": in.ProxyProtocol != nil, "transport": in.Transport != nil, "http": in.HTTP != nil,
		"http2": in.HTTP2 != nil, "http3": in.HTTP3 != nil, "observability": in.Observability != nil,
	} {
		if set {
			bad.add(field, "this setting has no effect on a UDP entrypoint")
		}
	}
	if in.UDP != nil {
		in.UDP.Timeout = duration(in.UDP.Timeout, "udp.timeout", false, bad)
	}
}

func buildTCP(in *models.EntryPointConfig, refs references, bad *issues) {
	if in.UDP != nil {
		bad.add("udp.timeout", "the UDP session timeout only applies to a UDP entrypoint")
	}
	if f := in.ForwardedHeaders; f != nil {
		trusted(f.TrustedIPs, f.Insecure, "forwarded_headers.trusted_ips", bad)
		if len(f.Connection) > 32 {
			bad.add("forwarded_headers.connection", "at most 32 Connection headers")
		}
		for _, h := range f.Connection {
			if !headerRegex.MatchString(h) {
				bad.add("forwarded_headers.connection", "'%s' is not a valid header name", h)
				break
			}
		}
		if f.Insecure {
			in.Warnings = append(in.Warnings, "Forwarded headers are trusted from every client. Use this for tests only.")
		}
	}
	if p := in.ProxyProtocol; p != nil {
		trusted(p.TrustedIPs, p.Insecure, "proxy_protocol.trusted_ips", bad)
		if p.Insecure {
			in.Warnings = append(in.Warnings, "PROXY protocol headers are accepted from every client. Use this for tests only.")
		}
		in.Warnings = append(in.Warnings, "PROXY protocol must also be enabled on the load balancer in front of Traefik, otherwise connections fail.")
	}
	if t := in.Transport; t != nil {
		t.ReadTimeout = duration(t.ReadTimeout, "transport.read_timeout", true, bad)
		t.WriteTimeout = duration(t.WriteTimeout, "transport.write_timeout", true, bad)
		t.IdleTimeout = duration(t.IdleTimeout, "transport.idle_timeout", true, bad)
		t.GraceTimeout = duration(t.GraceTimeout, "transport.grace_timeout", true, bad)
		t.RequestAcceptGraceTimeout = duration(t.RequestAcceptGraceTimeout, "transport.request_accept_grace_timeout", true, bad)
		t.KeepAliveMaxTime = duration(t.KeepAliveMaxTime, "transport.keep_alive_max_time", true, bad)
		if t.KeepAliveMaxRequests < 0 || t.KeepAliveMaxRequests > math.MaxInt32 {
			bad.add("transport.keep_alive_max_requests", "must be 0 (no limit) or a positive number")
		}
	}
	if h := in.HTTP; h != nil {
		buildHTTP(in, h, refs, bad)
	}
	if h := in.HTTP2; h != nil {
		for field, v := range map[string]int{"http2.max_concurrent_streams": h.MaxConcurrentStreams, "http2.max_decoder_header_table_size": h.MaxDecoderHeaderTableSize, "http2.max_encoder_header_table_size": h.MaxEncoderHeaderTableSize} {
			if v < 0 || v > math.MaxInt32 {
				bad.add(field, "must be a positive number")
			}
		}
	}
	if h := in.HTTP3; h != nil {
		if h.AdvertisedPort < 0 || h.AdvertisedPort > 65535 {
			bad.add("http3.advertised_port", "port must be between 1 and 65535")
		}
		in.Warnings = append(in.Warnings, fmt.Sprintf("HTTP/3 also listens on UDP port %d: publish %d/udp next to %d/tcp. Only routers with TLS are served over HTTP/3.", in.Port, in.Port, in.Port))
	}
	if o := in.Observability; o != nil && o.TraceVerbosity != "" && o.TraceVerbosity != "detailed" {
		bad.add("observability.trace_verbosity", "trace verbosity must be minimal or detailed")
	}
}

func buildHTTP(in *models.EntryPointConfig, h *models.EntryPointHTTP, refs references, bad *issues) {
	if r := h.Redirect; r != nil {
		switch m := portOnlyRegex.FindStringSubmatch(r.To); {
		case m != nil:
			if port, _ := strconv.Atoi(m[1]); port < 1 || port > 65535 {
				bad.add("http.redirect.to", "port must be between 1 and 65535")
			} else if port == in.Port {
				bad.add("http.redirect.to", "an entrypoint cannot redirect to its own port")
			}
		case r.To == "":
			bad.add("http.redirect.to", "choose the entrypoint (or :port) to redirect to")
		case r.To == in.Name:
			bad.add("http.redirect.to", "an entrypoint cannot redirect to itself")
		default:
			if target, ok := refs.Peers[r.To]; !ok {
				bad.add("http.redirect.to", "entrypoint '%s' does not exist", r.To)
			} else if target.Protocol != ProtocolTCP {
				bad.add("http.redirect.to", "entrypoint '%s' is a UDP entrypoint", r.To)
			}
		}
		if r.Scheme != "" && r.Scheme != "http" {
			bad.add("http.redirect.scheme", "scheme must be https or http")
		}
		if r.Priority < 0 || r.Priority > math.MaxInt32 {
			bad.add("http.redirect.priority", "priority must be a positive number")
		}
	}
	if len(h.Middlewares) > 32 {
		bad.add("http.middlewares", "at most 32 middlewares")
	}
	for _, mw := range h.Middlewares {
		if !middlewareRegex.MatchString(mw) {
			bad.add("http.middlewares", "'%s' must be a full name such as auth@file", mw)
			break
		}
		if name, isFile := strings.CutSuffix(mw, "@file"); isFile && !refs.Middlewares[name] {
			in.Warnings = append(in.Warnings, fmt.Sprintf("Middleware '%s' is not managed by the console; make sure it exists, otherwise every router of this entrypoint fails.", mw))
		}
	}
	if tls := h.TLS; tls != nil {
		if tls.Options != "" && !tlsOptionRegex.MatchString(tls.Options) {
			bad.add("http.tls.options", "TLS options must be a name such as default or modern@file")
		}
		if tls.CertResolver != "" && !refs.Resolvers[tls.CertResolver] {
			bad.add("http.tls.cert_resolver", "certificate resolver '%s' does not exist", tls.CertResolver)
		}
		if len(tls.Domains) > 0 && tls.CertResolver == "" {
			bad.add("http.tls.domains", "domains need a certificate resolver to request the certificate")
		}
		for _, d := range tls.Domains {
			if !domainRegex.MatchString(d.Main) || slices.ContainsFunc(d.SANs, func(s string) bool { return !domainRegex.MatchString(s) }) {
				bad.add("http.tls.domains", "every domain needs a valid main name; alternative names must be valid too")
				break
			}
		}
		if tls.CertResolver == "" {
			in.Warnings = append(in.Warnings, "TLS is on without a certificate resolver: Traefik serves a matching uploaded certificate, or its self-signed default.")
		}
	}
	for _, c := range h.DeniedEncodedCharacters {
		if _, ok := EncodedCharacters[c]; !ok {
			bad.add("http.denied_encoded_characters", "'%s' is not a known encoded character", c)
			break
		}
	}
	if h.MaxHeaderBytes < 0 || h.MaxHeaderBytes > math.MaxInt32 {
		bad.add("http.max_header_bytes", "must be a positive number of bytes")
	}
	if s := h.AliasHeadersStrategy; s != "" && s != "delete" && s != "reject" {
		bad.add("http.alias_headers_strategy", "strategy must be keep, delete or reject")
	}
	if h.DisableSanitizePath {
		in.Warnings = append(in.Warnings, "Path sanitization is off: requests with /../ or // reach routers and services unchanged. This is unsafe.")
	}
}

func trusted(ips []string, insecure bool, field string, bad *issues) {
	if insecure && len(ips) > 0 {
		bad.add(field, "trusted IPs have no effect while every source is trusted; remove them or turn 'trust all' off")
		return
	}
	if len(ips) > 64 {
		bad.add(field, "at most 64 addresses")
	}
	for _, ip := range ips {
		if _, _, err := net.ParseCIDR(ip); err != nil && net.ParseIP(ip) == nil {
			bad.add(field, "'%s' is not an IP address or CIDR range", ip)
			return
		}
	}
}

// duration accepts a Go duration ("90s", "1m30s") or a bare number of seconds, as Traefik does.
func duration(value, field string, allowZero bool, bad *issues) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if n, err := strconv.ParseInt(value, 10, 64); err == nil {
		value = strconv.FormatInt(n, 10) + "s"
	}
	d, err := time.ParseDuration(value)
	if err != nil || d < 0 || d > 24*time.Hour || (d == 0 && !allowZero) {
		if allowZero {
			bad.add(field, "use a duration such as 30s or 2m (0 disables the limit), at most 24h")
		} else {
			bad.add(field, "use a duration greater than zero, such as 3s, at most 24h")
		}
	}
	return value
}

// checkPortConflicts refuses two listeners on the same port and protocol. HTTP/3 also takes the UDP port.
func checkPortConflicts(in *models.EntryPointConfig, refs references, bad *issues) {
	overlap := func(host string) bool { return in.Host == "" || host == "" || strings.EqualFold(in.Host, host) }
	for name, other := range refs.Peers {
		if name == in.Name || other.Port != in.Port || !overlap(other.Host) {
			continue
		}
		switch {
		case other.Protocol == in.Protocol && !in.ReusePort:
			bad.add("port", "port %d/%s is already used by entrypoint '%s'", in.Port, in.Protocol, name)
		case in.Protocol == ProtocolTCP && in.HTTP3 != nil && other.Protocol == ProtocolUDP:
			bad.add("http3", "HTTP/3 needs UDP port %d, which entrypoint '%s' already uses", in.Port, name)
		case in.Protocol == ProtocolUDP && other.Protocol == ProtocolTCP && other.HTTP3:
			bad.add("port", "UDP port %d is already used by HTTP/3 on entrypoint '%s'", in.Port, name)
		}
	}
}

func formatAddress(host string, port int, protocol string) string {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	if protocol == ProtocolUDP {
		address += "/udp"
	}
	return address
}

// render produces the Traefik `entryPoints.<name>` map. Defaults are left out.
func render(c models.EntryPointConfig) map[string]any {
	out := map[string]any{"address": formatAddress(c.Host, c.Port, c.Protocol)}
	flag := func(m map[string]any, key string, on bool) {
		if on {
			m[key] = true
		}
	}
	text := func(m map[string]any, key, v string) {
		if v != "" {
			m[key] = v
		}
	}
	number := func(m map[string]any, key string, v int) {
		if v != 0 {
			m[key] = v
		}
	}
	section := func(key string, m map[string]any) {
		if len(m) > 0 {
			out[key] = m
		}
	}
	flag(out, "asDefault", c.AsDefault)
	flag(out, "reusePort", c.ReusePort)
	flag(out, "allowACMEByPass", c.AllowACMEByPass)

	if f := c.ForwardedHeaders; f != nil {
		m := map[string]any{}
		flag(m, "insecure", f.Insecure)
		flag(m, "notAppendXForwardedFor", f.NotAppendXForwardedFor)
		if len(f.TrustedIPs) > 0 {
			m["trustedIPs"] = f.TrustedIPs
		}
		if len(f.Connection) > 0 {
			m["connection"] = f.Connection
		}
		section("forwardedHeaders", m)
	}
	if p := c.ProxyProtocol; p != nil {
		m := map[string]any{}
		flag(m, "insecure", p.Insecure)
		if len(p.TrustedIPs) > 0 {
			m["trustedIPs"] = p.TrustedIPs
		}
		section("proxyProtocol", m)
	}
	if t := c.Transport; t != nil {
		m, responding, life := map[string]any{}, map[string]any{}, map[string]any{}
		text(responding, "readTimeout", t.ReadTimeout)
		text(responding, "writeTimeout", t.WriteTimeout)
		text(responding, "idleTimeout", t.IdleTimeout)
		text(life, "graceTimeOut", t.GraceTimeout)
		text(life, "requestAcceptGraceTimeout", t.RequestAcceptGraceTimeout)
		if len(responding) > 0 {
			m["respondingTimeouts"] = responding
		}
		if len(life) > 0 {
			m["lifeCycle"] = life
		}
		text(m, "keepAliveMaxTime", t.KeepAliveMaxTime)
		number(m, "keepAliveMaxRequests", t.KeepAliveMaxRequests)
		section("transport", m)
	}
	if h := c.HTTP; h != nil {
		m := map[string]any{}
		if r := h.Redirect; r != nil {
			target := map[string]any{"to": r.To}
			text(target, "scheme", r.Scheme)
			if r.Temporary {
				target["permanent"] = false
			}
			number(target, "priority", r.Priority)
			m["redirections"] = map[string]any{"entryPoint": target}
		}
		if len(h.Middlewares) > 0 {
			m["middlewares"] = h.Middlewares
		}
		if tls := h.TLS; tls != nil {
			t := map[string]any{}
			text(t, "options", tls.Options)
			text(t, "certResolver", tls.CertResolver)
			if len(tls.Domains) > 0 {
				domains := make([]map[string]any, 0, len(tls.Domains))
				for _, d := range tls.Domains {
					domain := map[string]any{"main": d.Main}
					if len(d.SANs) > 0 {
						domain["sans"] = d.SANs
					}
					domains = append(domains, domain)
				}
				t["domains"] = domains
			}
			// An empty mapping is meaningful: it turns TLS on.
			m["tls"] = t
		}
		if len(h.DeniedEncodedCharacters) > 0 {
			denied := map[string]any{}
			for _, c := range h.DeniedEncodedCharacters {
				denied[EncodedCharacters[c]] = false
			}
			m["encodedCharacters"] = denied
		}
		flag(m, "encodeQuerySemicolons", h.EncodeQuerySemicolons)
		if h.DisableSanitizePath {
			m["sanitizePath"] = false
		}
		number(m, "maxHeaderBytes", h.MaxHeaderBytes)
		text(m, "aliasHeadersStrategy", h.AliasHeadersStrategy)
		section("http", m)
	}
	if h := c.HTTP2; h != nil {
		m := map[string]any{}
		number(m, "maxConcurrentStreams", h.MaxConcurrentStreams)
		number(m, "maxDecoderHeaderTableSize", h.MaxDecoderHeaderTableSize)
		number(m, "maxEncoderHeaderTableSize", h.MaxEncoderHeaderTableSize)
		section("http2", m)
	}
	if h := c.HTTP3; h != nil {
		m := map[string]any{}
		number(m, "advertisedPort", h.AdvertisedPort)
		// An empty mapping is meaningful: it turns HTTP/3 on.
		out["http3"] = m
	}
	if u := c.UDP; u != nil {
		out["udp"] = map[string]any{"timeout": u.Timeout}
	}
	if o := c.Observability; o != nil {
		m := map[string]any{}
		if o.DisableAccessLogs {
			m["accessLogs"] = false
		}
		if o.DisableMetrics {
			m["metrics"] = false
		}
		if o.DisableTracing {
			m["tracing"] = false
		}
		text(m, "traceVerbosity", o.TraceVerbosity)
		section("observability", m)
	}
	return out
}
