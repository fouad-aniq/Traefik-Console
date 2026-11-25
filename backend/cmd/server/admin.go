package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/traefik-console/internal/auth"
	"github.com/traefik-console/internal/config"
)

const adminUsage = `usage: server admin <command>

  status                      show whether the administrator exists
  create [--username NAME]    create the administrator (only when none exists)
  reset-password              set a new administrator password; ends every session
  setup-token                 print a new one-time browser setup token (only when no administrator exists)
  reset --yes                 remove the administrator, all sessions and sign-in providers;
                              the console returns to first-run setup

Passwords are read from the terminal without echo, or as one line from standard
input when it is not a terminal. They are never accepted as arguments or
environment variables. These commands work while the console is running.`

func runAdmin(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Println(adminUsage)
		return nil
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--password") || a == "-p" {
			return errors.New("passwords are never accepted as arguments; you will be prompted (or pipe it on standard input)")
		}
	}
	opts, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	store, svc, err := openAuth(opts, nil)
	if err != nil {
		return fmt.Errorf("authentication: %w", err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch args[0] {
	case "status":
		name, err := svc.AdminInfo(ctx)
		if err != nil {
			return err
		}
		if name == "" {
			cliOut.Infof("no administrator yet: open the console and run server admin setup-token, or run: server admin create")
			return nil
		}
		cliOut.Infof("administrator: %s", name)
		providers, err := svc.Providers(ctx)
		if err != nil {
			return err
		}
		if len(providers) == 0 {
			cliOut.Infof("external sign-in: none")
		}
		for _, p := range providers {
			cliOut.Infof("external sign-in: %s (%s, id %s)", p.DisplayName, p.Kind, p.ID)
		}
		cliOut.Infof("auth database: %s", opts.Auth.DBPath)
		return nil

	case "create":
		username, err := usernameArg(args[1:])
		if err != nil {
			return err
		}
		if exists, err := svc.AdminExists(ctx); err != nil {
			return err
		} else if exists {
			return errors.New("an administrator already exists; use: server admin reset-password")
		}
		if username == "" {
			if username, err = promptLine("Administrator username: "); err != nil {
				return err
			}
		}
		if err = auth.ValidateUsername(strings.TrimSpace(username)); err != nil {
			return err
		}
		password, err := readNewPassword()
		if err != nil {
			return err
		}
		if err = svc.CreateAdmin(ctx, username, password); err != nil {
			return err
		}
		cliOut.Infof("administrator %q created; sign in from the browser", strings.TrimSpace(username))
		return nil

	case "reset-password":
		if len(args) > 1 {
			return errors.New("reset-password takes no arguments")
		}
		name, err := svc.AdminInfo(ctx)
		if err != nil {
			return err
		}
		if name == "" {
			return errors.New("no administrator exists yet; use: server admin create")
		}
		fmt.Fprintf(os.Stderr, "Setting a new password for %q.\n", name)
		password, err := readNewPassword()
		if err != nil {
			return err
		}
		if err = svc.ResetPassword(ctx, password); err != nil {
			return err
		}
		cliOut.Infof("password updated; every session has been signed out")
		return nil

	case "setup-token":
		token, err := svc.MintSetupToken(ctx)
		if errors.Is(err, auth.ErrSetupComplete) {
			return errors.New("an administrator already exists; a setup token is not needed")
		}
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(os.Stdout, token)
		return err

	case "reset":
		if len(args) != 2 || args[1] != "--yes" {
			return errors.New("this removes the administrator, every session and external sign-in; confirm with: server admin reset --yes")
		}
		_, err := svc.ResetAll(ctx)
		if err != nil {
			return err
		}
		cliOut.Infof("Authentication reset. Run server admin setup-token locally to create an administrator.")
		return nil

	default:
		return fmt.Errorf("unknown admin command %q\n\n%s", args[0], adminUsage)
	}
}

func usernameArg(args []string) (string, error) {
	switch {
	case len(args) == 0:
		return "", nil
	case len(args) == 2 && args[0] == "--username":
		return args[1], nil
	case len(args) == 1 && strings.HasPrefix(args[0], "--username="):
		return strings.TrimPrefix(args[0], "--username="), nil
	default:
		return "", errors.New("usage: server admin create [--username NAME]")
	}
}

func stdinIsTerminal() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

var stdinReader = bufio.NewReader(io.LimitReader(os.Stdin, 4096))

func promptLine(prompt string) (string, error) {
	if stdinIsTerminal() {
		fmt.Fprint(os.Stderr, prompt)
	}
	line, err := stdinReader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", errors.New("no input")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// readNewPassword prompts twice without echo on a terminal, or reads one line
// from a pipe (for automation: printf '%s\n' "$PW" | server admin reset-password).
func readNewPassword() (string, error) {
	if !stdinIsTerminal() {
		password, err := promptLine("")
		if err != nil {
			return "", err
		}
		return password, auth.ValidatePassword(password)
	}
	first, err := readHidden(fmt.Sprintf("New password (at least %d characters): ", auth.MinPasswordLength))
	if err != nil {
		return "", err
	}
	if err = auth.ValidatePassword(first); err != nil {
		return "", err
	}
	second, err := readHidden("Repeat the password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("the passwords do not match")
	}
	return first, nil
}

func readHidden(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if err := stty("-echo"); err != nil {
		return "", errors.New("cannot hide terminal input; pipe the password on standard input instead")
	}
	defer func() {
		_ = stty("echo")
		fmt.Fprintln(os.Stderr)
	}()
	line, err := stdinReader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return "", errors.New("no input")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func stty(arg string) error {
	cmd := exec.Command("stty", arg)
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
