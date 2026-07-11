package provision

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// SSHParams are the request-only SSH credentials used to provision a node.
// They are never persisted; ongoing control is mTLS.
type SSHParams struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	KeyPEM   string `json:"key_pem"`
}

// NewSSHRunner builds an SSHRunner from params: it writes a private key (if
// given) to a 0600 temp file and pins the host key. The returned cleanup
// removes the temp key; call it when done.
func NewSSHRunner(ctx context.Context, p SSHParams, knownHosts string) (*SSHRunner, func(), error) {
	cleanup := func() {}
	r := &SSHRunner{
		Host: p.Host, User: p.User, Port: p.Port, Password: p.Password,
		KnownHosts: knownHosts, ConnectTimeout: 15 * time.Second,
	}
	if p.KeyPEM != "" {
		f, err := os.CreateTemp("", "tp-ssh-key-*")
		if err != nil {
			return nil, cleanup, err
		}
		_ = f.Chmod(0o600)
		// OpenSSH refuses a PEM private key that lacks a trailing newline
		// ("error in libcrypto"). Clients commonly drop it — shell $(...) capture
		// strips trailing newlines, UI textareas trim — so normalize here.
		key := strings.TrimRight(p.KeyPEM, "\n") + "\n"
		if _, err := f.WriteString(key); err != nil {
			f.Close()
			os.Remove(f.Name())
			return nil, cleanup, err
		}
		f.Close()
		r.KeyFile = f.Name()
		cleanup = func() { os.Remove(f.Name()) }
	}
	if err := r.EnsureHostKey(ctx); err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("pin host key: %w", err)
	}
	return r, cleanup, nil
}
