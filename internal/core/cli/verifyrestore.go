package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"trustpanel/internal/core/backup"
	"trustpanel/internal/core/model"
)

// RunVerifyRestore is the verify-restore drill ("Layer 4"): it loads the latest
// local snapshot into a throwaway Postgres and validates the PKI, proving the
// backup is actually restorable — without touching the live database. Driven by a
// systemd timer on the control-plane node; a failure raises an alert. On success
// it stamps a marker file so a missed drill (timer broken) is detectable.
func RunVerifyRestore(args []string) {
	fs := flag.NewFlagSet("verify-restore", flag.ExitOnError)
	dir := fs.String("dir", "/var/backups/trustpanel", "backup dir to pick the newest snapshot from")
	fromFile := fs.String("from-file", "", "verify this specific snapshot instead of the newest")
	pgBinDir := fs.String("pg-bin-dir", "", "dir holding initdb/pg_ctl/psql (default: discover)")
	dsn := fs.String("dsn", "", "Postgres DSN for alert-bot config (or TRUSTPANEL_DSN); optional")
	marker := fs.String("marker", "/var/lib/trustpanel/last-verify", "file stamped on a successful drill (\"\" to skip)")
	noAlert := fs.Bool("no-alert", false, "do not raise an alert-bot notification on failure")
	keepTemp := fs.Bool("keep-temp", false, "leave the temporary restore dir for debugging")
	scheduled := fs.Bool("scheduled", false, "honor the panel-configured verify interval/toggle and skip if not due (set by the timer; manual runs omit it)")
	_ = fs.Parse(args)

	connDSN := *dsn
	if connDSN == "" {
		connDSN = os.Getenv("TRUSTPANEL_DSN")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	// Alert config lives in the DB; the drill works without it (log-only) so it can
	// still run when the DB is unhealthy — which is exactly when you want the drill.
	var settings model.Settings
	var notify func(string)
	if connDSN != "" {
		st, s, settingsErr := loadBackupSettings(ctx, connDSN)
		if st != nil {
			defer st.Close()
		}
		settings = s
		host, _ := os.Hostname()
		notify = backupNotifier(ctx, chooseBackupAlerter(s, settingsErr, *noAlert), host)
	} else {
		notify = func(msg string) { log.Printf("backup: %s", msg) }
	}

	// A scheduled run (timer) honors the panel-set drill policy from the replicated
	// settings — the toggle and interval — keyed off the marker's age. A manual run
	// (no --scheduled) always proceeds. Settings unreadable -> defaults (run).
	if *scheduled {
		bc := settings.Backup
		if !bc.VerifyOn() {
			log.Printf("verify-restore: drill disabled in panel settings — scheduled run skipped")
			return
		}
		age, have := markerAge(*marker)
		interval := bc.VerifyInterval()
		if !backupDue(age, have, interval) {
			log.Printf("verify-restore: not due yet (last drill %s ago, interval %s) — scheduled run skipped", age.Round(time.Minute), interval)
			return
		}
	}

	rep, err := backup.VerifyRestore(ctx, backup.VerifyOptions{
		SnapshotPath:  *fromFile,
		Dir:           *dir,
		PgBinDir:      *pgBinDir,
		RunAsPostgres: os.Geteuid() == 0, // PG refuses to run as root
		KeepTemp:      *keepTemp,
	})
	if err != nil {
		notify(fmt.Sprintf("⚠️ TrustPanel verify-restore FAILED: %v", err))
		log.Fatalf("verify-restore failed: %v", err)
	}

	log.Printf("verify-restore OK: %s", reportLine(rep))
	if *marker != "" {
		if err := writeMarker(*marker, rep); err != nil {
			log.Printf("verify-restore: could not write marker %s: %v", *marker, err)
		}
	}
}

// markerAge reports how long ago the last successful drill stamped the marker and
// whether it exists. A missing/unreadable marker means "no record" -> due.
func markerAge(path string) (time.Duration, bool) {
	if path == "" {
		return 0, false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

func reportLine(r *backup.VerifyReport) string {
	var tbls []string
	for t, n := range r.TableRows {
		tbls = append(tbls, fmt.Sprintf("%s=%d", t, n))
	}
	sort.Strings(tbls)
	exp := "n/a"
	if !r.CAExpiry.IsZero() {
		exp = r.CAExpiry.Format("2006-01-02")
	}
	return fmt.Sprintf("snapshot=%s db_bytes=%d migration=%s pki_ok=%t ca_expires=%s rows[%s] took=%s",
		r.Snapshot, r.DBBytes, r.MigrationVersion, r.PKIChecked, exp, strings.Join(tbls, " "), r.Duration.Round(time.Millisecond))
}

func writeMarker(path string, r *backup.VerifyReport) error {
	if d := dirOf(path); d != "" {
		_ = os.MkdirAll(d, 0o755)
	}
	body := fmt.Sprintf("%s\n%s\n", time.Now().UTC().Format(time.RFC3339), reportLine(r))
	return os.WriteFile(path, []byte(body), 0o644)
}

func dirOf(path string) string {
	if i := strings.LastIndexByte(path, '/'); i > 0 {
		return path[:i]
	}
	return ""
}
