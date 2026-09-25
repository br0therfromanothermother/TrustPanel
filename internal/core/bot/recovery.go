package bot

import (
	"context"
	"time"

	"trustpanel/internal/core/journal"
	"trustpanel/internal/core/model"
	"trustpanel/internal/core/recovery"
)

// recoverCooldown keeps a stuck finger (or a bored operator) from minting codes
// in a loop: each issue invalidates the previous one and writes an audit entry,
// so the churn is worth damping even though the flow is authorized.
const recoverCooldown = time.Minute

// recoverConfirm renders the confirmation step for panel-password recovery. The
// code is not minted yet; this only explains what the next tap does, so a
// mis-tap in the account menu costs nothing.
func (b *Bot) recoverConfirm(ctx context.Context, acct model.AdminAccount) (string, string) {
	if !acct.Role.IsAdmin() {
		return tr(ctx, "The panel is an admin-only surface — your account manages clients through this bot."),
			inlineKeyboard([][]ikBtn{backRow(ctx)})
	}
	text := trf(ctx, "🔑 Recover panel access\n\nThis issues a one-time code for \"%s\", valid %d minutes. You enter it on the panel's sign-in screen to set a new password.\n\nAny code issued earlier stops working.", acct.Username, recovery.TTLMinutes)
	rows := [][]ikBtn{
		{{tr(ctx, "✅ Issue code"), "pwrec:go"}},
		backTo(ctx, "Account", "m:acct"),
	}
	return text, inlineKeyboard(rows)
}

// recoverIssue mints a one-time recovery code for the calling account and
// returns it in the chat. Only the account bound to this Telegram id can ask,
// and only an admin: an operator has no panel login to recover.
func (b *Bot) recoverIssue(ctx context.Context, acct model.AdminAccount, fromID int64) (string, string) {
	back := inlineKeyboard([][]ikBtn{backRow(ctx)})
	if !acct.Role.IsAdmin() {
		return tr(ctx, "The panel is an admin-only surface — your account manages clients through this bot."), back
	}
	now := b.now()
	if last, err := b.store.LastRecoveryIssue(ctx, acct.Username); err == nil && !last.IsZero() {
		if wait := recoverCooldown - now.Sub(last); wait > 0 {
			return trf(ctx, "A code was just issued. Try again in %d seconds.", int(wait.Seconds())+1), back
		}
	}
	code, err := recovery.NewCode()
	if err != nil {
		return errMsg(err), back
	}
	expires := now.Add(recovery.TTLMinutes * time.Minute)
	if err := b.store.IssueRecoveryCode(ctx, acct.Username, recovery.Hash(code), fromID, expires); err != nil {
		return errMsg(err), back
	}
	// A recovery code is a credential event: record it so the panel's log shows
	// who asked and when, even if the code is never redeemed.
	b.recordFor(ctx, model.SeverityWarn, journal.Line("panel recovery code issued for %q", acct.Username), acct)
	text := trf(ctx, "🔑 Recovery code for \"%s\"\n\n%s\n\nValid %d minutes, single use. On the panel sign-in screen choose \"Forgot password\", then enter this code and your new password.\n\nIf you did not ask for this, someone has access to this chat — rotate the bot token in the panel.", acct.Username, code, recovery.TTLMinutes)
	return text, back
}
