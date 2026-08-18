package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
)

func inviteCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: fledge invite <create|list|revoke>")
	}

	switch args[0] {
	case "create":
		return inviteCreate(ctx, args[1:])
	case "list", "ls":
		return inviteList(ctx, args[1:])
	case "revoke", "rm":
		return inviteRevoke(ctx, args[1:])
	default:
		return fmt.Errorf("unknown invite command %q", args[0])
	}
}

func inviteCreate(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("invite create", flag.ExitOnError)
	note := flags.String("note", "", "who this is for, so the list is readable later")
	expires := flags.String("expires", "168h", "how long it stays open")
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	invite, err := api.CreateInvite(ctx, *note, *expires)
	if err != nil {
		return err
	}

	fmt.Printf("Invitation for %s\n", dash(invite.Note))
	fmt.Printf("  expires  %s\n", invite.Expires.Format("2006-01-02 15:04"))
	fmt.Printf("\nSend this. It registers one device, then it is spent:\n  %s\n", invite.URL)

	return nil
}

func inviteList(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("invite list", flag.ExitOnError)
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	invites, err := api.Invites(ctx)
	if err != nil {
		return err
	}
	if len(invites) == 0 {
		fmt.Println("no invitations yet")
		return nil
	}

	out := table()
	row(out, "ID\tFOR\tSTATE\tEXPIRES\tUSED BY\n")
	for _, invite := range invites {
		used := "-"
		if invite.UsedBy != "" {
			used = invite.UsedBy
		}
		row(out, "%s\t%s\t%s\t%s\t%s\n",
			invite.ID[:12], dash(invite.Note), invite.State,
			remaining(invite.Expires), used)
	}

	return out.Flush()
}

func inviteRevoke(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("invite revoke", flag.ExitOnError)
	server := flags.String("server", "", "Fledge server URL")
	token := flags.String("token", "", "Fledge upload token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: fledge invite revoke <id>")
	}

	api, err := newClient(*server, *token)
	if err != nil {
		return err
	}

	if err := api.RevokeInvite(ctx, flags.Arg(0)); err != nil {
		return err
	}
	fmt.Println("revoked")

	return nil
}
