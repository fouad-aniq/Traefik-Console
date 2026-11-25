package middlewares

import (
	"testing"

	"github.com/traefik-console/internal/models"
)

func router(name string, mws ...string) models.UIResource[models.RouterConfig] {
	return models.UIResource[models.RouterConfig]{Name: name, Config: models.RouterConfig{Rule: "Host(`" + name + ".example`)", Service: name, Middlewares: mws}}
}

func chain(name string, members ...string) models.UIResource[models.ChainConfig] {
	return models.UIResource[models.ChainConfig]{Name: name, Config: models.ChainConfig{Middlewares: members}}
}

func TestUsageMatchesBareAndFileReferencesOnly(t *testing.T) {
	routers := []models.UIResource[models.RouterConfig]{router("b", "auth@file"), router("a", "auth"), router("docker-auth", "auth@docker"), router("none")}
	chains := []models.UIResource[models.ChainConfig]{chain("secure", "headers", "auth"), chain("other", "headers")}
	u := Usage("auth", routers, chains)
	if len(u.Routers) != 2 || u.Routers[0].Name != "a" || u.Routers[1].Name != "b" {
		t.Fatal("routers (sorted, @file aliases the bare name, @docker is another middleware)", u.Routers)
	}
	if len(u.Chains) != 1 || u.Chains[0].Name != "secure" || len(u.Chains[0].Members) != 2 {
		t.Fatal("chains", u.Chains)
	}
	if ext := Usage("auth@docker", routers, chains); len(ext.Routers) != 1 || ext.Routers[0].Name != "docker-auth" {
		t.Fatal("external middleware usage", ext.Routers)
	}
}

func TestUsageOfAChainIgnoresItselfAndIsNeverNil(t *testing.T) {
	chains := []models.UIResource[models.ChainConfig]{chain("secure", "secure-inner"), chain("outer", "secure")}
	u := Usage("secure", []models.UIResource[models.RouterConfig]{router("app", "secure@file")}, chains)
	if len(u.Chains) != 1 || u.Chains[0].Name != "outer" || len(u.Routers) != 1 {
		t.Fatal(u)
	}
	empty := Usage("unused", nil, nil)
	if empty.Routers == nil || empty.Chains == nil {
		t.Fatal("empty usage must serialize as [] not null")
	}
}
