// Command trustpanel is the single binary for the control plane. It
// dispatches subcommands:
//
//	trustpanel serve     # operator panel (localhost HTTP API over Postgres)
//	trustpanel agent     # per-node mTLS agent + reconcile
//	trustpanel ca        # fleet PKI (init/controller/node/sign)
//	trustpanel fallback  # camouflage fallback origin site
package main

import (
	"fmt"
	"os"

	"trustpanel/internal/core/cli"
	"trustpanel/internal/fallback"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "serve":
		cli.RunServe(os.Args[2:])
	case "agent":
		cli.RunAgent(os.Args[2:])
	case "ca":
		cli.RunCA(os.Args[2:])
	case "watchdog":
		cli.RunWatchdog(os.Args[2:])
	case "promote":
		cli.RunPromote(os.Args[2:])
	case "cluster":
		cli.RunCluster(os.Args[2:])
	case "provision":
		cli.RunProvision(os.Args[2:])
	case "bot":
		cli.RunBot(os.Args[2:])
	case "bootstrap":
		cli.RunBootstrap(os.Args[2:])
	case "backup":
		cli.RunBackup(os.Args[2:])
	case "restore":
		cli.RunRestore(os.Args[2:])
	case "verify-restore":
		cli.RunVerifyRestore(os.Args[2:])
	case "fallback":
		if err := fallback.Command(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "fallback:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintln(os.Stderr, "trustpanel: unknown command "+os.Args[1])
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: trustpanel bootstrap|serve|agent|ca|watchdog|promote|cluster|provision|bot|backup|restore|verify-restore|fallback [flags]")
	os.Exit(2)
}
