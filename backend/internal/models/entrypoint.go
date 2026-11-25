package models

// EntryPointConfig is the request/stored model of a managed entrypoint (traefik.yml `entryPoints.<name>`).
// It mirrors the Traefik v3.7 static EntryPoint. Only values that differ from Traefik's defaults are
// rendered, so a simple entrypoint stays a one-line `address`.
type EntryPointConfig struct {
	Name string `json:"name"`
	// Kind says what the entrypoint is for: "http" (web), "https" (web with TLS for every router),
	// "tcp" (a plain TCP service) or "udp". It decides which options apply and is checked against
	// them; it is not written to traefik.yml. Empty means "work it out from the settings".
	Kind string `json:"kind,omitempty"`
	// Protocol is "tcp" (HTTP and TCP routers) or "udp". It cannot change after creation.
	Protocol string `json:"protocol"`
	// Host is the optional bind IP or hostname; empty listens on every interface.
	Host string `json:"host,omitempty"`
	Port int    `json:"port"`
	// Address is the rendered `[host]:port[/udp]` (response only).
	Address string `json:"address,omitempty"`

	AsDefault       bool `json:"as_default,omitempty"`
	ReusePort       bool `json:"reuse_port,omitempty"`
	AllowACMEByPass bool `json:"allow_acme_bypass,omitempty"`

	ForwardedHeaders *EntryPointForwardedHeaders `json:"forwarded_headers,omitempty"`
	ProxyProtocol    *EntryPointProxyProtocol    `json:"proxy_protocol,omitempty"`
	Transport        *EntryPointTransport        `json:"transport,omitempty"`
	HTTP             *EntryPointHTTP             `json:"http,omitempty"`
	HTTP2            *EntryPointHTTP2            `json:"http2,omitempty"`
	HTTP3            *EntryPointHTTP3            `json:"http3,omitempty"`
	UDP              *EntryPointUDP              `json:"udp,omitempty"`
	Observability    *EntryPointObservability    `json:"observability,omitempty"`

	// Adopted marks an entrypoint that already existed in traefik.yml and was taken over by the
	// console. Original keeps its definition as it was, so releasing it (delete, or restoring an
	// older snapshot) puts that definition back instead of removing the listener.
	Adopted  bool           `json:"adopted,omitempty"`
	Original map[string]any `json:"original,omitempty"`

	// Adoptable and AdoptBlocker describe a read-only entrypoint found in traefik.yml (response only).
	Adoptable    bool   `json:"adoptable,omitempty"`
	AdoptBlocker string `json:"adopt_blocker,omitempty"`
	// Usage lists what references the entrypoint (response only).
	Usage *EntryPointUsage `json:"usage,omitempty"`

	Warnings []string `json:"warnings,omitempty"`
	// RenderedNode is the validated YAML map the generator writes at entryPoints.<name>.
	RenderedNode map[string]any `json:"rendered_node,omitempty"`
}

// EntryPointForwardedHeaders decides whose X-Forwarded-* headers are trusted.
type EntryPointForwardedHeaders struct {
	Insecure               bool     `json:"insecure,omitempty"`
	TrustedIPs             []string `json:"trusted_ips,omitempty"`
	Connection             []string `json:"connection,omitempty"`
	NotAppendXForwardedFor bool     `json:"not_append_x_forwarded_for,omitempty"`
}

// EntryPointProxyProtocol decides who may send a PROXY protocol header.
type EntryPointProxyProtocol struct {
	Insecure   bool     `json:"insecure,omitempty"`
	TrustedIPs []string `json:"trusted_ips,omitempty"`
}

// EntryPointTransport holds timeouts as Go durations ("60s"). Empty means Traefik's default.
type EntryPointTransport struct {
	ReadTimeout               string `json:"read_timeout,omitempty"`
	WriteTimeout              string `json:"write_timeout,omitempty"`
	IdleTimeout               string `json:"idle_timeout,omitempty"`
	GraceTimeout              string `json:"grace_timeout,omitempty"`
	RequestAcceptGraceTimeout string `json:"request_accept_grace_timeout,omitempty"`
	KeepAliveMaxTime          string `json:"keep_alive_max_time,omitempty"`
	KeepAliveMaxRequests      int    `json:"keep_alive_max_requests,omitempty"`
}

type EntryPointHTTP struct {
	Redirect    *EntryPointRedirect `json:"redirect,omitempty"`
	Middlewares []string            `json:"middlewares,omitempty"`
	TLS         *EntryPointTLS      `json:"tls,omitempty"`
	// DeniedEncodedCharacters lists the encoded path characters to reject: slash, backSlash,
	// nullCharacter, semicolon, percent, questionMark, hash. Traefik allows all of them by default.
	DeniedEncodedCharacters []string `json:"denied_encoded_characters,omitempty"`
	EncodeQuerySemicolons   bool     `json:"encode_query_semicolons,omitempty"`
	// DisableSanitizePath turns off path normalization (sanitizePath: false). Unsafe.
	DisableSanitizePath  bool   `json:"disable_sanitize_path,omitempty"`
	MaxHeaderBytes       int    `json:"max_header_bytes,omitempty"`
	AliasHeadersStrategy string `json:"alias_headers_strategy,omitempty"` // keep | delete | reject
}

// EntryPointRedirect sends every request of the entrypoint to another entrypoint or port.
type EntryPointRedirect struct {
	To     string `json:"to"`               // entrypoint name or ":port"
	Scheme string `json:"scheme,omitempty"` // https (default) | http
	// Temporary sends a 302 instead of Traefik's default permanent redirect.
	Temporary bool `json:"temporary,omitempty"`
	Priority  int  `json:"priority,omitempty"`
}

// EntryPointTLS turns TLS on for every HTTP router of the entrypoint.
type EntryPointTLS struct {
	Options      string                `json:"options,omitempty"`
	CertResolver string                `json:"cert_resolver,omitempty"`
	Domains      []EntryPointTLSDomain `json:"domains,omitempty"`
}

type EntryPointTLSDomain struct {
	Main string   `json:"main"`
	SANs []string `json:"sans,omitempty"`
}

type EntryPointHTTP2 struct {
	MaxConcurrentStreams      int `json:"max_concurrent_streams,omitempty"`
	MaxDecoderHeaderTableSize int `json:"max_decoder_header_table_size,omitempty"`
	MaxEncoderHeaderTableSize int `json:"max_encoder_header_table_size,omitempty"`
}

// EntryPointHTTP3 enables HTTP/3 (QUIC, over UDP on the same port number).
type EntryPointHTTP3 struct {
	AdvertisedPort int `json:"advertised_port,omitempty"`
}

type EntryPointUDP struct {
	Timeout string `json:"timeout,omitempty"`
}

// EntryPointObservability switches the per-entrypoint defaults off; Traefik enables all three.
type EntryPointObservability struct {
	DisableAccessLogs bool   `json:"disable_access_logs,omitempty"`
	DisableMetrics    bool   `json:"disable_metrics,omitempty"`
	DisableTracing    bool   `json:"disable_tracing,omitempty"`
	TraceVerbosity    string `json:"trace_verbosity,omitempty"` // minimal | detailed
}

// EntryPointUsage names the resources that reference an entrypoint.
type EntryPointUsage struct {
	Routers     []string `json:"routers,omitempty"`
	Resolvers   []string `json:"resolvers,omitempty"`
	EntryPoints []string `json:"entry_points,omitempty"` // entrypoints redirecting to it
}
