package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
	"time"

	"github.com/theoutdoorprogrammer/fledge/internal/ipa"
)

func inspectCommand(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ExitOnError)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: fledge inspect <file.ipa>")
	}

	app, err := ipa.Open(flags.Arg(0))
	if err != nil {
		return err
	}

	out := table()
	row(out, "name\t%s\n", app.Name)
	row(out, "bundle\t%s\n", app.BundleID)
	row(out, "version\t%s (%s)\n", app.Version, app.Build)
	row(out, "size\t%d bytes\n", app.Size)
	row(out, "sha256\t%s\n", app.SHA256)
	if app.MinimumOS != "" {
		row(out, "requires\tiOS %s\n", app.MinimumOS)
	}
	row(out, "icon\t%s\n", describeIcon(len(app.Icon)))

	if profile := app.Profile; profile != nil {
		row(out, "profile\t%s (%s)\n", profile.Type, profile.Name)
		row(out, "team\t%s (%s)\n", profile.TeamName, profile.TeamID)
		row(out, "expires\t%s (%s)\n", profile.Expires.Format("2006-01-02"), remaining(profile.Expires))
		if profile.ProvisionsAllDevices {
			row(out, "devices\tany device\n")
		} else {
			row(out, "devices\t%d registered\n", len(profile.Devices))
		}
		row(out, "dev mode\t%t\n", app.RequiresDeveloperMode())
	} else {
		row(out, "profile\tnone embedded\n")
	}

	return out.Flush()
}

func appsCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("apps", flag.ExitOnError)
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	apps, err := api.Apps(ctx)
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		fmt.Println("nothing published yet")
		return nil
	}

	out := table()
	row(out, "BUNDLE\tNAME\tLATEST\tBUILDS\tEXPIRES\n")
	for _, app := range apps {
		expires := "-"
		name, latest := "-", "-"
		if app.Latest != nil {
			name = app.Latest.App.Name
			latest = app.Latest.App.Version + " (" + app.Latest.App.Build + ")"
			if profile := app.Latest.App.Profile; profile != nil {
				expires = remaining(profile.Expires)
			}
		}
		row(out, "%s\t%s\t%s\t%d\t%s\n", app.BundleID, name, latest, app.Builds, expires)
	}

	return out.Flush()
}

func buildsCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("builds", flag.ExitOnError)
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: fledge builds <bundle-id>")
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	builds, err := api.Builds(ctx, flags.Arg(0))
	if err != nil {
		return err
	}

	out := table()
	row(out, "BUILD\tVERSION\tPROFILE\tUPLOADED\tEXPIRES\n")
	for _, build := range builds {
		profileType, expires := "-", "-"
		if profile := build.App.Profile; profile != nil {
			profileType = string(profile.Type)
			expires = remaining(profile.Expires)
		}
		row(out, "%s\t%s (%s)\t%s\t%s\t%s\n",
			build.ID, build.App.Version, build.App.Build,
			profileType, build.Uploaded.Format("2006-01-02 15:04"), expires)
	}

	return out.Flush()
}

func devicesCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("devices", flag.ExitOnError)
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	devices, err := api.Devices(ctx)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		fmt.Println("no devices enrolled yet")
		return nil
	}

	out := table()
	row(out, "NAME\tUDID\tPRODUCT\tOS\tWITH APPLE\n")
	for _, device := range devices {
		row(out, "%s\t%s\t%s\t%s\t%t\n",
			device.Name, device.UDID, dash(device.Product), dash(device.OSVersion), device.Registered)
	}

	return out.Flush()
}

func table() *tabwriter.Writer {
	return tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
}

// row writes one line into a table. The tabwriter only buffers here, so Flush
// is the call that can actually fail.
func row(out io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(out, format, args...)
}

// remaining renders how long is left in whole days, which is the unit that
// matters for a provisioning profile.
func remaining(deadline time.Time) string {
	if deadline.IsZero() {
		return "-"
	}

	days := int(time.Until(deadline).Hours() / 24)
	switch {
	case days < 0:
		return "expired"
	case days == 0:
		return "today"
	case days == 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", days)
	}
}

func describeIcon(size int) string {
	if size == 0 {
		return "none extracted"
	}
	return fmt.Sprintf("%d bytes", size)
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
