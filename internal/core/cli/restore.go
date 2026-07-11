package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trustpanel/internal/core/backup"
)

// RunRestore has two modes:
//
//   - file-level (default): reassemble the age-encrypted parts downloaded from
//     the Telegram backup channel and decrypt them into a plaintext snapshot
//     tar.gz. Runs on the operator's machine with the off-fleet age identity.
//
//   - --apply: take a plaintext snapshot and load it ONTO a freshly bootstrapped
//     control plane (recreate the DB + restore the PKI). Runs as root on the new
//     box. It does the deterministic, destructive mechanics only — claim
//     leadership afterwards with `trustpanel promote --node-id <this>
//     --start-serve` (no --pg-promote; the DB is already primary).
func RunRestore(args []string) {
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	apply := fs.Bool("apply", false, "apply a plaintext snapshot onto this node (recreate DB + restore PKI)")
	// file-level mode
	dir := fs.String("from-file", "", "directory holding the downloaded *.age.partNNN files")
	identity := fs.String("identity", "", "age identity (private key) file from age-keygen")
	out := fs.String("out", "", "output path for the decrypted snapshot tar.gz")
	// apply mode
	from := fs.String("from", "", "[--apply] plaintext snapshot tar.gz to apply")
	dsn := fs.String("dsn", "", "[--apply] app Postgres DSN (or TRUSTPANEL_DSN)")
	pkiDir := fs.String("pki-dir", "/etc/trustpanel/pki", "[--apply] where to restore pki/*")
	owner := fs.String("owner", "trustpanel:trustpanel", "[--apply] chown owner for restored PKI")
	force := fs.Bool("force", false, "[--apply] overwrite even if the target DB already has fleet data")
	noStopServe := fs.Bool("no-stop-serve", false, "[--apply] do not stop trustpanel-serve before reloading")
	_ = fs.Parse(args)

	if *apply {
		runApply(*from, *dsn, *pkiDir, *owner, *force, !*noStopServe)
		return
	}

	if *dir == "" || *identity == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: trustpanel restore --from-file <dir> --identity <age-key> --out <snapshot.tar.gz>")
		fmt.Fprintln(os.Stderr, "   or: trustpanel restore --apply --from <snapshot.tar.gz> --dsn <dsn> [--force]")
		os.Exit(2)
	}

	path, err := backup.RestoreFile(*dir, *identity, *out)
	if err != nil {
		log.Fatalf("restore failed: %v", err)
	}
	log.Printf("restored snapshot: %s", path)
	log.Printf("next: trustpanel restore --apply --from %s --dsn <dsn>  (on the fresh control plane)", path)
}

func runApply(from, dsn, pkiDir, owner string, force, stopServe bool) {
	connDSN := dsn
	if connDSN == "" {
		connDSN = os.Getenv("TRUSTPANEL_DSN")
	}
	if from == "" || connDSN == "" {
		fmt.Fprintln(os.Stderr, "usage: trustpanel restore --apply --from <snapshot.tar.gz> --dsn <dsn> [--force]")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	rep, err := backup.ApplyRestore(ctx, backup.ApplyOptions{
		SnapshotPath: from, DSN: connDSN, PKIDir: pkiDir, Owner: owner,
		Force: force, StopServe: stopServe,
	})
	if err != nil {
		log.Fatalf("restore --apply failed: %v", err)
	}
	log.Printf("restore --apply OK: db=%s owner=%s pki_files=%d prior_nodes=%d took=%s",
		rep.DBName, rep.Owner, rep.PKIFiles, rep.PriorNodes, rep.Duration.Round(time.Millisecond))
	log.Printf("next: claim leadership + start the panel on this node:")
	log.Printf("  trustpanel promote --node-id <this-node-id> --start-serve")
	log.Printf("  (do NOT pass --pg-promote: the database is already primary, not a replica)")
}
