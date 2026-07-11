package cli

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trustpanel/internal/core/backup"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/store"
	"trustpanel/internal/core/watchdog"
)

// RunBackup writes one disaster-recovery snapshot (Postgres dump + fleet PKI) to
// a local directory and prunes old ones. Driven by a systemd timer on the
// control-plane node; can also be run by hand.
//
// Because it normally runs unattended (the daily timer), any failure — the local
// dump or the off-site push — is also raised via the alert bot, so a silently
// broken backup chain surfaces before it is needed in a disaster.
func RunBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (or set TRUSTPANEL_DSN)")
	outDir := fs.String("out-dir", "/var/backups/trustpanel", "directory for backup snapshots")
	keep := fs.Int("keep", 0, "number of snapshots to retain (older pruned); 0 = use the panel setting (default 14)")
	scheduled := fs.Bool("scheduled", false, "honor the panel-configured interval/enabled toggle and skip if not due (set by the timer; manual runs omit it and always run)")
	pkiDir := fs.String("pki-dir", "/etc/trustpanel/pki", "fleet PKI dir to include (CA key etc.)")
	noPKI := fs.Bool("no-pki", false, "exclude the PKI from the snapshot")
	pgDump := fs.String("pg-dump-bin", "pg_dump", "pg_dump binary")
	noTelegram := fs.Bool("no-telegram", false, "skip off-site Telegram delivery even if configured")
	noAlert := fs.Bool("no-alert", false, "do not raise an alert-bot notification on failure")
	chunkBytes := fs.Int("chunk-bytes", 0, "override max bytes per Telegram part (0 = default ~45MiB)")
	_ = fs.Parse(args)

	connDSN := *dsn
	if connDSN == "" {
		connDSN = os.Getenv("TRUSTPANEL_DSN")
	}
	if connDSN == "" {
		log.Fatal("backup: --dsn or TRUSTPANEL_DSN is required")
	}
	pki := *pkiDir
	if *noPKI {
		pki = ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Load settings up front: they hold both the off-site config and the alert-bot
	// credentials. We keep the store open for the whole run so a failure can be
	// reported even if it happens during the dump. If the DB itself is unreachable
	// settings load fails and we fall back to log-only (we then can't alert via the
	// DB-stored bot token anyway).
	st, settings, settingsErr := loadBackupSettings(ctx, connDSN)
	if st != nil {
		defer st.Close()
	}
	host, _ := os.Hostname()
	notify := backupNotifier(ctx, chooseBackupAlerter(settings, settingsErr, *noAlert), host)

	// A scheduled run (timer) honors the panel-set policy from the replicated
	// settings: the local toggle and the interval. Both nodes read the same
	// settings, so one panel edit governs the active node and the standby. A manual
	// run (no --scheduled) ignores this and always backs up now. If settings could
	// not be loaded we fall through to the defaults rather than skip — losing a
	// backup is worse than an extra one.
	if *scheduled {
		bc := settings.Backup
		if !bc.LocalOn() {
			log.Printf("backup: local backup disabled in panel settings — scheduled run skipped")
			return
		}
		age, have := newestSnapshotAge(*outDir)
		interval := bc.BackupInterval()
		if !backupDue(age, have, interval) {
			log.Printf("backup: not due yet (last snapshot %s ago, interval %s) — scheduled run skipped", age.Round(time.Minute), interval)
			return
		}
	}

	keepN := *keep
	if keepN <= 0 {
		keepN = settings.Backup.KeepOrDefault()
	}

	path, err := backup.Create(ctx, backup.Options{
		DSN: connDSN, OutDir: *outDir, PKIDir: pki, Keep: keepN, PgDumpBin: *pgDump,
	})
	if err != nil {
		notify(watchdog.Render(watchdog.MsgBackupFailed(fmt.Sprint(err)), watchdog.DefaultLang))
		log.Fatalf("backup failed: %v", err)
	}
	log.Printf("backup written: %s (retaining %d)", path, keepN)

	if *noTelegram {
		return
	}
	if settingsErr != nil {
		log.Printf("backup: off-site skipped: load settings: %v", settingsErr)
		return
	}
	if !settings.Backup.Enabled {
		return
	}
	chunk := *chunkBytes
	if chunk <= 0 {
		chunk = settings.Backup.ChunkBytes
	}
	if err := backup.DeliverTelegram(ctx, path,
		backup.TelegramTarget{Token: settings.Alert.Token, ChatID: settings.Backup.ChatID},
		backup.DeliverOptions{AgeRecipient: settings.Backup.AgeRecipient, ChunkBytes: chunk}); err != nil {
		// Off-site is opportunistic for the local snapshot's sake, but a persistent
		// off-site failure means the fleet has no disaster copy — so it alerts.
		notify(watchdog.Render(watchdog.MsgBackupOffsiteFailed(fmt.Sprint(err)), watchdog.DefaultLang))
		log.Printf("backup: off-site delivery FAILED (local snapshot still written): %v", err)
		return
	}
	log.Printf("backup: delivered off-site to Telegram chat %s", settings.Backup.ChatID)
}

// newestSnapshotAge reports how long ago the most recent local snapshot was
// written and whether one exists. Errors (empty/missing dir) are treated as "no
// snapshot" so a scheduled run proceeds rather than skips.
func newestSnapshotAge(dir string) (time.Duration, bool) {
	path, err := backup.LatestSnapshot(dir)
	if err != nil {
		return 0, false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// backupDue decides whether a scheduled backup should run now: yes if none exists
// yet, otherwise once the newest is roughly one interval old. The slack absorbs
// timer jitter (the timer fires hourly) so a ~daily interval lands near the mark
// instead of slipping an extra hour each day.
func backupDue(age time.Duration, have bool, interval time.Duration) bool {
	if !have {
		return true
	}
	slack := 30 * time.Minute
	if interval/2 < slack {
		slack = interval / 2
	}
	return age >= interval-slack
}

// loadBackupSettings opens the store and reads settings. The store handle is
// returned (caller-closed) so it stays alive for the run; on any error the store
// is closed here and a nil handle + zero settings + the error are returned.
func loadBackupSettings(ctx context.Context, dsn string) (*store.Store, model.Settings, error) {
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return nil, model.Settings{}, fmt.Errorf("open store: %w", err)
	}
	s, err := st.GetSettings(ctx)
	if err != nil {
		st.Close()
		return nil, model.Settings{}, fmt.Errorf("load settings: %w", err)
	}
	return st, s, nil
}

// chooseBackupAlerter decides whether failures should reach the alert bot. It
// returns nil (log-only) when alerts are disabled (--no-alert), settings could
// not be loaded, or the alert bot is not fully configured.
func chooseBackupAlerter(s model.Settings, settingsErr error, noAlert bool) watchdog.Alerter {
	if noAlert || settingsErr != nil {
		return nil
	}
	a := s.Alert
	if !a.Enabled || a.Token == "" || a.ChatID == "" {
		return nil // alert bot not configured -> journald is the only record
	}
	return watchdog.TelegramAlerter{Token: a.Token, ChatID: a.ChatID}
}

// backupNotifier returns a function that always logs the message and, when an
// alerter is configured, also pushes it to the alert bot. It is always safe to
// call (a nil alerter means log-only).
func backupNotifier(ctx context.Context, alerter watchdog.Alerter, host string) func(string) {
	return func(msg string) {
		if host != "" {
			msg = msg + "\nhost: " + watchdog.Code(host)
		}
		log.Printf("backup: %s", watchdog.PlainText(msg))
		if alerter == nil {
			return
		}
		// A broken backup/off-site chain is something the operator must act on -> ring.
		if err := alerter.Alert(ctx, watchdog.SeverityCritical, msg); err != nil {
			log.Printf("backup: alert send failed: %v", err)
		}
	}
}
