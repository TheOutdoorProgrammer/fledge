package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/nerdswhofish/fledge/internal/asc"
)

func appleCommand(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("apple", flag.ExitOnError)
	issuer := flags.String("issuer", "", "App Store Connect issuer ID")
	keyID := flags.String("key-id", "", "App Store Connect key ID")
	keyFile := flags.String("key", "", "path to the .p8 private key")
	if err := flags.Parse(args); err != nil {
		return err
	}

	client, err := appleClient(*issuer, *keyID, *keyFile)
	if err != nil {
		return err
	}

	devices, err := client.Devices(ctx)
	if err != nil {
		return err
	}

	printCapacity(devices)
	printDevices(devices)

	return nil
}

// appleClient resolves credentials from flags then the same environment
// variables the server reads, so the two are configured identically.
func appleClient(issuer, keyID, keyFile string) (*asc.Client, error) {
	if issuer == "" {
		issuer = os.Getenv("FLEDGE_ASC_ISSUER_ID")
	}
	if keyID == "" {
		keyID = os.Getenv("FLEDGE_ASC_KEY_ID")
	}

	var key []byte
	switch {
	case keyFile != "":
		raw, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, err
		}
		key = raw
	case os.Getenv("FLEDGE_ASC_PRIVATE_KEY_FILE") != "":
		raw, err := os.ReadFile(os.Getenv("FLEDGE_ASC_PRIVATE_KEY_FILE"))
		if err != nil {
			return nil, err
		}
		key = raw
	case os.Getenv("FLEDGE_ASC_PRIVATE_KEY") != "":
		key = []byte(os.Getenv("FLEDGE_ASC_PRIVATE_KEY"))
	}

	if keyID == "" || len(key) == 0 {
		return nil, errors.New("need an App Store Connect key: pass -issuer, -key-id and -key, or set FLEDGE_ASC_*")
	}

	return asc.New(issuer, keyID, key)
}

// printCapacity is the number that matters, because Apple never returns a slot
// once it is spent.
func printCapacity(devices []asc.Device) {
	counts := map[string]int{}
	for _, device := range devices {
		class := device.DeviceType
		if class == "" {
			class = device.Platform
		}
		counts[class]++
	}

	classes := make([]string, 0, len(counts))
	for class := range counts {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	out := table()
	row(out, "TYPE\tUSED\tLIMIT\tFREE\n")
	for _, class := range classes {
		used := counts[class]
		row(out, "%s\t%d\t%d\t%d\n", class, used, asc.DeviceLimit, asc.DeviceLimit-used)
	}
	_ = out.Flush()

	fmt.Println("\nA slot is not returned when a device is removed. It resets only at renewal.")
}

func printDevices(devices []asc.Device) {
	if len(devices) == 0 {
		return
	}

	sort.Slice(devices, func(i, j int) bool { return devices[i].Name < devices[j].Name })

	fmt.Println()
	out := table()
	row(out, "NAME\tUDID\tTYPE\tSTATUS\n")
	for _, device := range devices {
		row(out, "%s\t%s\t%s\t%s\n", device.Name, device.UDID, dash(device.DeviceType), device.Status)
	}
	_ = out.Flush()
}
