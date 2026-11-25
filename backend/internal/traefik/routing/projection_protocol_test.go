package routing

import (
	"errors"
	"reflect"
	"testing"

	"github.com/traefik-console/internal/models"
)

const mixedStatic = "entryPoints:\n  web: {address: ':80'}\n  websecure: {address: ':443'}\n  db: {address: ':5432'}\n  dns: {address: ':53/udp'}\ncertificatesResolvers:\n  le: {acme: {tlsChallenge: {}}}\n"

func tcp(c models.RouterConfig) models.RouterConfig { c.Protocol = "tcp"; return c }
func udp(c models.RouterConfig) models.RouterConfig { c.Protocol = "udp"; return c }

func TestStaticTellsTCPEntrypointsFromUDPOnes(t *testing.T) {
	s := registered(t, mixedStatic)
	for name, want := range map[string]EntryPoint{"web": {TCP: true}, "db": {TCP: true}, "dns": {UDP: true}} {
		if s.EntryPoints[name] != want {
			t.Errorf("%s = %+v, want %+v", name, s.EntryPoints[name], want)
		}
	}
}

func TestProjectATCPRouterMatchesBySNI(t *testing.T) {
	static := registered(t, mixedStatic)
	cases := []struct {
		name string
		in   models.RouterConfig
		rule string
	}{
		{"one host", tcp(models.RouterConfig{Domains: []string{" Vault.Example.com "}, Service: "vault", TLS: &models.RouterTLS{Enabled: true, Passthrough: true}}), "HostSNI(`vault.example.com`)"},
		{"several hosts", tcp(models.RouterConfig{Domains: []string{"a.example.com", "b.example.com"}, Service: "s", TLS: &models.RouterTLS{Enabled: true, Passthrough: true}}), "(HostSNI(`a.example.com`) || HostSNI(`b.example.com`))"},
		{"every connection", tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "pg"}), "HostSNI(`*`)"},
		{"custom rule wins", tcp(models.RouterConfig{Domains: []string{"a.example.com"}, CustomRule: " HostSNIRegexp(`^.+\\.example\\.com$`) ", Service: "s", TLS: &models.RouterTLS{Enabled: true}}), "HostSNIRegexp(`^.+\\.example\\.com$`)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.EntryPoints = []string{"db"}
			got, err := Project(c.in, nil, nil, static)
			if err != nil {
				t.Fatal(err)
			}
			if got.Rule != c.rule || got.Protocol != "tcp" || got.Middlewares != nil {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestProjectATCPRouterKeepsPassthroughAndTLSApart(t *testing.T) {
	static := registered(t, mixedStatic)
	pass, err := Project(tcp(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Passthrough: true}}), nil, nil, static)
	if err != nil || pass.TLS == nil || !pass.TLS.Enabled || !pass.TLS.Passthrough {
		t.Fatalf("passthrough implies TLS: %+v %v", pass.TLS, err)
	}
	term, err := Project(tcp(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Enabled: true, CertResolver: "le", Options: "modern"}}), nil, nil, static)
	if err != nil || term.TLS.Passthrough || term.TLS.CertResolver != "le" {
		t.Fatalf("%+v %v", term.TLS, err)
	}
	// TLS switched off and nothing else set is no TLS at all.
	plain, err := Project(tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"db"}, TLS: &models.RouterTLS{}}), nil, nil, static)
	if err != nil || plain.TLS != nil {
		t.Fatalf("%+v %v", plain.TLS, err)
	}
}

func TestProjectATCPRouterBoundToAManagedDomainTerminatesTLS(t *testing.T) {
	static := registered(t, mixedStatic)
	domain := &models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "le"}
	got, err := Project(tcp(models.RouterConfig{DomainName: "site", Subdomain: "mqtt", Service: "mqtt", EntryPoints: []string{"websecure"}}), domain, nil, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rule != "HostSNI(`mqtt.example.com`)" || got.TLS == nil || got.TLS.CertResolver != "le" || got.TLS.Passthrough {
		t.Fatalf("%+v", got)
	}
}

func TestProjectRefusesWhatATCPRouterCannotDo(t *testing.T) {
	static := registered(t, mixedStatic)
	domain := &models.DomainConfig{Main: "example.com", Method: "resolver", Resolver: "le"}
	cases := map[string]struct {
		in     models.RouterConfig
		domain *models.DomainConfig
	}{
		"a path to match":                 {tcp(models.RouterConfig{Domains: []string{"a.example.com"}, PathPrefix: "/api", Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Enabled: true}}), nil},
		"no backend":                      {tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: models.NoopService, EntryPoints: []string{"db"}}), nil},
		"no service":                      {tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", EntryPoints: []string{"db"}}), nil},
		"nothing to match":                {tcp(models.RouterConfig{Service: "s", EntryPoints: []string{"db"}}), nil},
		"a hostname without TLS":          {tcp(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"db"}}), nil},
		"a wildcard hostname":             {tcp(models.RouterConfig{Domains: []string{"*.example.com"}, Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Enabled: true}}), nil},
		"a UDP entrypoint":                {tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"dns"}}), nil},
		"an unknown entrypoint":           {tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"ghost"}}), nil},
		"passthrough with a resolver":     {tcp(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Enabled: true, Passthrough: true, CertResolver: "le"}}), nil},
		"passthrough with TLS options":    {tcp(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Passthrough: true, Options: "modern"}}), nil},
		"passthrough on a managed domain": {tcp(models.RouterConfig{DomainName: "site", Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Passthrough: true}}), domain},
		"an unregistered certificate":     {tcp(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"websecure"}, TLS: &models.RouterTLS{Enabled: true, CertResolver: "missing"}}), nil},
		"a priority out of range":         {tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"db"}, Priority: 1 << 60}), nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Project(c.in, c.domain, nil, static); !errors.Is(err, ErrInvalidRouter) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestProjectPicksTheEntrypointOfATCPOrUDPRouterOnlyWhenUnambiguous(t *testing.T) {
	tcpCfg := tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "s"})
	udpCfg := udp(models.RouterConfig{Service: "s"})

	// One entrypoint of the right kind is the choice, whatever the other kinds are.
	only := registered(t, "entryPoints:\n  web: {address: ':80'}\n  dns: {address: ':53/udp'}\n")
	if got, err := Project(tcpCfg, nil, nil, only); err != nil || !reflect.DeepEqual(got.EntryPoints, []string{"web"}) {
		t.Fatalf("%+v %v", got.EntryPoints, err)
	}
	if got, err := Project(udpCfg, nil, nil, only); err != nil || !reflect.DeepEqual(got.EntryPoints, []string{"dns"}) {
		t.Fatalf("%+v %v", got.EntryPoints, err)
	}
	// Several: refused unless one is flagged as the default.
	many := registered(t, mixedStatic)
	for name, cfg := range map[string]models.RouterConfig{"tcp": tcpCfg, "udp": udp(models.RouterConfig{Service: "s"})} {
		if name == "udp" {
			many = registered(t, "entryPoints:\n  a: {address: ':53/udp'}\n  b: {address: ':54/udp'}\n")
		}
		if _, err := Project(cfg, nil, nil, many); !errors.Is(err, ErrInvalidRouter) {
			t.Fatalf("%s: a guess was made between several entrypoints: %v", name, err)
		}
	}
	flagged := registered(t, "entryPoints:\n  a: {address: ':53/udp'}\n  b: {address: ':54/udp', asDefault: true}\n  c: {address: ':80'}\n")
	if got, err := Project(udpCfg, nil, nil, flagged); err != nil || !reflect.DeepEqual(got.EntryPoints, []string{"b"}) {
		t.Fatalf("%+v %v", got.EntryPoints, err)
	}
	// None of the right kind: refused.
	if _, err := Project(udpCfg, nil, nil, registered(t, "entryPoints:\n  web: {address: ':80'}\n")); !errors.Is(err, ErrInvalidRouter) {
		t.Fatal(err)
	}
}

func TestProjectAUDPRouterIsEntryPointsAndAServiceOnly(t *testing.T) {
	static := registered(t, mixedStatic)
	got, err := Project(udp(models.RouterConfig{Service: " dns ", EntryPoints: []string{" dns "}}), nil, nil, static)
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "udp" || got.Service != "dns" || got.Rule != "" || got.Domains != nil || got.Middlewares != nil || got.TLS != nil || !reflect.DeepEqual(got.EntryPoints, []string{"dns"}) {
		t.Fatalf("%+v", got)
	}
	cases := map[string]models.RouterConfig{
		"a rule":           {Rule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"dns"}},
		"a custom rule":    {CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"dns"}},
		"hostnames":        {Domains: []string{"a.example.com"}, Service: "s", EntryPoints: []string{"dns"}},
		"a managed domain": {DomainName: "site", Service: "s", EntryPoints: []string{"dns"}},
		"a path":           {PathPrefix: "/a", Service: "s", EntryPoints: []string{"dns"}},
		"middlewares":      {Middlewares: []string{"m"}, Service: "s", EntryPoints: []string{"dns"}},
		"TLS":              {TLS: &models.RouterTLS{Enabled: true}, Service: "s", EntryPoints: []string{"dns"}},
		"a priority":       {Priority: 3, Service: "s", EntryPoints: []string{"dns"}},
		"no service":       {EntryPoints: []string{"dns"}},
		"no backend":       {Service: models.NoopService, EntryPoints: []string{"dns"}},
		"a TCP entrypoint": {Service: "s", EntryPoints: []string{"db"}},
		"an unknown entry": {Service: "s", EntryPoints: []string{"ghost"}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Project(udp(in), nil, nil, static); !errors.Is(err, ErrInvalidRouter) {
				t.Fatalf("accepted: %v", err)
			}
		})
	}
}

func TestProjectKeepsHTTPRoutersAsTheyWere(t *testing.T) {
	static := registered(t, mixedStatic)
	got, err := Project(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "app"}, nil, nil, static)
	if err != nil || got.Protocol != "" || got.Rule != "Host(`a.example.com`)" || !reflect.DeepEqual(got.EntryPoints, []string{"web"}) {
		t.Fatalf("%+v %v", got, err)
	}
	// "http" spelled out is the same router.
	same, err := Project(models.RouterConfig{Protocol: "http", Domains: []string{"a.example.com"}, Service: "app"}, nil, nil, static)
	if err != nil || !reflect.DeepEqual(got, same) {
		t.Fatalf("%+v %v", same, err)
	}
	// An HTTP router cannot use a UDP entrypoint or pass TLS through.
	if _, err := Project(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "app", EntryPoints: []string{"dns"}}, nil, nil, static); !errors.Is(err, ErrInvalidRouter) {
		t.Fatal(err)
	}
	if _, err := Project(models.RouterConfig{Domains: []string{"a.example.com"}, Service: "app", TLS: &models.RouterTLS{Enabled: true, Passthrough: true}}, nil, nil, static); !errors.Is(err, ErrInvalidRouter) {
		t.Fatal(err)
	}
}

func TestProjectRefusesAnUnknownProtocol(t *testing.T) {
	static := registered(t, mixedStatic)
	for _, p := range []string{"sctp", "HTTP", "tcp ", "grpc"} {
		if _, err := Project(models.RouterConfig{Protocol: p, Domains: []string{"a.example.com"}, Service: "app"}, nil, nil, static); !errors.Is(err, ErrInvalidRouter) {
			t.Errorf("protocol %q accepted: %v", p, err)
		}
	}
}

func TestCompileTCPRule(t *testing.T) {
	for _, c := range []struct {
		domains []string
		custom  string
		want    string
	}{
		{nil, "", ""},
		{[]string{"", "  "}, "", ""},
		{[]string{"A.example.com"}, "", "HostSNI(`a.example.com`)"},
		{[]string{"a.example.com", "b.example.com"}, "", "(HostSNI(`a.example.com`) || HostSNI(`b.example.com`))"},
		{[]string{"a.example.com"}, "  HostSNI(`*`) ", "HostSNI(`*`)"},
	} {
		if got := CompileTCPRule(c.domains, c.custom); got != c.want {
			t.Errorf("%v %q = %q, want %q", c.domains, c.custom, got, c.want)
		}
	}
}

func TestATCPRouterKeepsItsMiddlewaresInOrder(t *testing.T) {
	static := registered(t, mixedStatic)
	got, err := Project(tcp(models.RouterConfig{CustomRule: "HostSNI(`*`)", Service: "s", EntryPoints: []string{"db"}, Middlewares: []string{" cap ", "office@docker"}}), nil, nil, static)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Middlewares) != 2 || got.Middlewares[0] != "cap" || got.Middlewares[1] != "office@docker" {
		t.Fatal(got.Middlewares)
	}
	// A UDP router still has none.
	if _, err := Project(models.RouterConfig{Protocol: "udp", Service: "s", EntryPoints: []string{"dns"}, Middlewares: []string{"cap"}}, nil, nil, static); err == nil {
		t.Fatal("a UDP router took a middleware")
	}
}
