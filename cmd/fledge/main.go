// Command fledge releases iOS builds to a Fledge server.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nerdswhofish/fledge/internal/client"
	"github.com/nerdswhofish/fledge/internal/version"
)

const usage = `fledge — release ad hoc iOS builds to your own devices

Usage:
  fledge release [flags]        Archive, export and publish the app in this directory
  fledge upload <file.ipa>      Publish an archive Xcode already exported
  fledge inspect <file.ipa>     Print what Fledge reads out of an archive
  fledge apps                   List published apps
  fledge builds <bundle-id>     List an app's builds
  fledge devices                List enrolled devices
  fledge version

The server and token come from FLEDGE_URL and FLEDGE_TOKEN, or from -server and
-token on the commands that talk to it.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := dispatch(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "fledge:", err)
		os.Exit(1)
	}
}

func dispatch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}

	command, rest := args[0], args[1:]
	switch command {
	case "release":
		return releaseCommand(ctx, rest)
	case "upload":
		return uploadCommand(ctx, rest)
	case "inspect":
		return inspectCommand(rest)
	case "apps":
		return appsCommand(ctx, rest)
	case "builds":
		return buildsCommand(ctx, rest)
	case "devices":
		return devicesCommand(ctx, rest)
	case "version":
		fmt.Println("fledge", version.String())
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try: fledge help)", command)
	}
}

// newClient resolves the server and token from flags then the environment.
func newClient(server, token string) (*client.Client, error) {
	if server == "" {
		server = os.Getenv("FLEDGE_URL")
	}
	if token == "" {
		token = os.Getenv("FLEDGE_TOKEN")
	}
	if server == "" {
		return nil, client.ErrNoServer
	}
	if token == "" {
		return nil, errors.New("no token: set FLEDGE_TOKEN or pass -token")
	}

	return client.New(server, token), nil
}
