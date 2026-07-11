package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"trustpanel/internal/core/model"
	"trustpanel/internal/core/watchdog"
)

type recordAlerter struct {
	msgs []string
	sevs []watchdog.Severity
}

func (r *recordAlerter) Alert(_ context.Context, sev watchdog.Severity, msg string) error {
	r.msgs = append(r.msgs, msg)
	r.sevs = append(r.sevs, sev)
	return nil
}

func configured() model.Settings {
	return model.Settings{Alert: model.AlertSettings{Enabled: true, Token: "tok", ChatID: "-100"}}
}

func TestChooseBackupAlerter(t *testing.T) {
	cases := []struct {
		name       string
		s          model.Settings
		settingErr error
		noAlert    bool
		wantNil    bool
	}{
		{"configured", configured(), nil, false, false},
		{"no-alert flag", configured(), nil, true, true},
		{"settings load failed", configured(), errors.New("db down"), false, true},
		{"alert disabled", model.Settings{Alert: model.AlertSettings{Enabled: false, Token: "t", ChatID: "-1"}}, nil, false, true},
		{"missing token", model.Settings{Alert: model.AlertSettings{Enabled: true, ChatID: "-1"}}, nil, false, true},
		{"missing chat", model.Settings{Alert: model.AlertSettings{Enabled: true, Token: "t"}}, nil, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooseBackupAlerter(c.s, c.settingErr, c.noAlert)
			if (got == nil) != c.wantNil {
				t.Fatalf("chooseBackupAlerter nil=%v want nil=%v", got == nil, c.wantNil)
			}
		})
	}
}

func TestBackupNotifierSendsAndTagsHost(t *testing.T) {
	rec := &recordAlerter{}
	notify := backupNotifier(context.Background(), rec, "exit1")
	notify("backup FAILED: boom")
	if len(rec.msgs) != 1 {
		t.Fatalf("want 1 alert, got %d", len(rec.msgs))
	}
	if !strings.Contains(rec.msgs[0], "boom") || !strings.Contains(rec.msgs[0], "host: ") || !strings.Contains(rec.msgs[0], "exit1") {
		t.Fatalf("alert missing detail/host: %q", rec.msgs[0])
	}
}

func TestBackupNotifierNilAlerterIsLogOnly(t *testing.T) {
	// A nil alerter must be safe to call (log-only) and not panic.
	notify := backupNotifier(context.Background(), nil, "")
	notify("nothing should be sent")
}

// Ensure the concrete TelegramAlerter still satisfies the interface used here.
var _ watchdog.Alerter = watchdog.TelegramAlerter{}

func TestBackupDue(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name     string
		age      time.Duration
		have     bool
		interval time.Duration
		want     bool
	}{
		{"no snapshot yet", 0, false, day, true},
		{"fresh -> not due", time.Hour, true, day, false},
		{"within slack of interval -> due", 23*time.Hour + 40*time.Minute, true, day, true},
		{"well past interval -> due", 30 * time.Hour, true, day, true},
		{"short interval respects half slack", 40 * time.Minute, true, time.Hour, true},
		{"short interval fresh -> not due", 10 * time.Minute, true, time.Hour, false},
	}
	for _, c := range cases {
		if got := backupDue(c.age, c.have, c.interval); got != c.want {
			t.Errorf("%s: backupDue(%s,%t,%s)=%t want %t", c.name, c.age, c.have, c.interval, got, c.want)
		}
	}
}

func TestBackupSettingsDefaults(t *testing.T) {
	var z model.BackupSettings // zero value (a row predating these fields)
	if !z.LocalOn() || !z.VerifyOn() {
		t.Fatalf("absent toggles must default ON, got local=%t verify=%t", z.LocalOn(), z.VerifyOn())
	}
	if z.KeepOrDefault() != 14 || z.BackupInterval() != 24*time.Hour || z.VerifyInterval() != 7*24*time.Hour {
		t.Fatalf("defaults wrong: keep=%d interval=%s verify=%s", z.KeepOrDefault(), z.BackupInterval(), z.VerifyInterval())
	}
	off := false
	d := model.BackupSettings{LocalEnabled: &off, IntervalHours: 6, Keep: 30, VerifyEnabled: &off, VerifyIntervalDays: 3}
	if d.LocalOn() || d.VerifyOn() || d.KeepOrDefault() != 30 || d.BackupInterval() != 6*time.Hour || d.VerifyInterval() != 3*24*time.Hour {
		t.Fatalf("explicit values not honored: %+v", d)
	}
}
