package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/traefik-console/internal/logging"
)

var (
	diagLog = logging.New(os.Stderr, logging.LevelInfo)
	cliOut  = logging.New(os.Stdout, logging.LevelInfo)
)

func main() {
	if err := runCommand(); err != nil {
		diagLog.Errorf("%v", err)
		os.Exit(1)
	}
}
func runCommand() error {
	cmd := "server"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cmd = os.Args[1]
	}
	switch cmd {
	case "validate-config":
		runValidateConfig()
		return nil
	case "preflight":
		return runPreflight()
	case "healthcheck":
		return runHealthCheck()
	case "snapshot":
		return runCLISnapshot(os.Args[2:], false)
	case "rollback":
		return runCLISnapshot(os.Args[2:], true)
	case "admin":
		return runAdmin(os.Args[2:])
	case "server":
		return runServer()
	default:
		return fmt.Errorf("unknown command %q; usage: %s [server|admin|preflight|healthcheck|validate-config|snapshot|rollback]", cmd, os.Args[0])
	}
}
