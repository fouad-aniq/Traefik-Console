package config

import (
	"fmt"
	"strings"
)

// DockerOptions is operator configuration only; the HTTP API cannot select an
// endpoint, target container, command or execution identity.
type DockerOptions struct {
	Host, TraefikContainer, CrowdSecContainer string
	AllowRestart                              bool
}

func loadDockerOptions() (DockerOptions, error) {
	for _, name := range []string{
		"BROKER_SOCKET",
		"BROKER_LISTEN_SOCKET",
		"BROKER_DOCKER_HOST",
		"BROKER_TRAEFIK_ID",
		"BROKER_CROWDSEC_ID",
		"BROKER_COMPOSE_PROJECT",
		"BROKER_TRAEFIK_SERVICE",
		"BROKER_CROWDSEC_SERVICE",
		"BROKER_ALLOW_RESTART",
		"BROKER_TRAEFIK_SECRETS_DIR",
	} {
		if getString(name, "") != "" {
			return DockerOptions{}, fmt.Errorf("legacy custom broker configuration is no longer supported; configure DOCKER_HOST and TRAEFIK_CONTAINER in the Console")
		}
	}

	for _, name := range []string{"TRAEFIK_CONTAINER_ID", "CROWDSEC_CONTAINER_ID", "DOCKER_COMPOSE_PROJECT", "TRAEFIK_COMPOSE_SERVICE", "CROWDSEC_COMPOSE_SERVICE"} {
		if getString(name, "") != "" {
			return DockerOptions{}, fmt.Errorf("%s is no longer used: set TRAEFIK_CONTAINER (and CROWDSEC_CONTAINER) to the container name", name)
		}
	}

	host := getString("DOCKER_HOST", "")
	traefik := getString("TRAEFIK_CONTAINER", "")
	if host != "" && traefik == "" {
		traefik = "traefik"
	}
	restart := getString("ALLOW_TRAEFIK_RESTART", "")
	if restart != "" && restart != "true" && restart != "false" {
		return DockerOptions{}, fmt.Errorf("ALLOW_TRAEFIK_RESTART must be true or false")
	}

	return DockerOptions{
		Host:              host,
		TraefikContainer:  traefik,
		CrowdSecContainer: getString("CROWDSEC_CONTAINER", ""),
		AllowRestart:      strings.EqualFold(restart, "true"),
	}, nil
}
