package provision

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SSHRunner is the production Runner: it executes commands and writes files over
// SSH with pinned host keys. SSH is used only for provisioning; ongoing control
// is mTLS. Not unit-tested (it shells out to ssh).
type SSHRunner struct {
	Host           string
	User           string
	Port           int
	KeyFile        string
	Password       string // if set, auth via sshpass (provisioning a fresh server)
	KnownHosts     string
	ConnectTimeout time.Duration
}

// prog returns the command and its leading args, wrapping with sshpass when a
// password is configured. sshpass reads the password from the SSHPASS env var
// (-e), never from argv (-p), so it is not visible in the process list; callers
// set that env via passwordEnv.
func (s SSHRunner) prog(sshArgs []string) (string, []string) {
	if s.Password != "" {
		return "sshpass", append([]string{"-e", "ssh"}, sshArgs...)
	}
	return "ssh", sshArgs
}

// passwordEnv returns the process environment with SSHPASS set when a password is
// configured (consumed by `sshpass -e`), else the plain environment.
func (s SSHRunner) passwordEnv() []string {
	if s.Password == "" {
		return os.Environ()
	}
	return append(os.Environ(), "SSHPASS="+s.Password)
}

func (s SSHRunner) sshArgs(extra ...string) []string {
	port := s.Port
	if port == 0 {
		port = 22
	}
	timeout := int(s.ConnectTimeout / time.Second)
	if timeout <= 0 {
		timeout = 10
	}
	args := []string{
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(timeout),
		"-p", strconv.Itoa(port),
	}
	if s.Password != "" {
		args = append(args, "-o", "PreferredAuthentications=password", "-o", "PubkeyAuthentication=no")
	} else {
		args = append(args, "-o", "BatchMode=yes")
	}
	if s.KnownHosts != "" {
		args = append(args, "-o", "UserKnownHostsFile="+s.KnownHosts)
	}
	if s.KeyFile != "" {
		args = append(args, "-i", s.KeyFile)
	}
	args = append(args, s.User+"@"+s.Host)
	return append(args, extra...)
}

// EnsureHostKey pins the host key via ssh-keyscan on first contact (TOFU), so
// subsequent connections use StrictHostKeyChecking=yes.
func (s SSHRunner) EnsureHostKey(ctx context.Context) error {
	if s.KnownHosts == "" {
		return fmt.Errorf("known_hosts file is required for host-key pinning")
	}
	if data, err := os.ReadFile(s.KnownHosts); err == nil && strings.Contains(string(data), s.Host) {
		return nil // already pinned
	}
	port := s.Port
	if port == 0 {
		port = 22
	}
	out, err := exec.CommandContext(ctx, "ssh-keyscan", "-p", strconv.Itoa(port), s.Host).Output()
	if err != nil {
		return fmt.Errorf("ssh-keyscan %s: %w", s.Host, err)
	}
	f, err := os.OpenFile(s.KnownHosts, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(out)
	return err
}

// needsSudo reports whether privileged actions must be wrapped in sudo: true
// when connecting as any non-root user. Provisioning needs root on the target;
// connecting as a passwordless-sudo user (NOPASSWD, as hardening sets up) is
// supported so the panel can provision without a direct root login.
func (s SSHRunner) needsSudo() bool {
	u := strings.TrimSpace(s.User)
	return u != "" && u != "root"
}

func (s SSHRunner) Run(ctx context.Context, cmd string) (string, error) {
	// Feed the command to a remote shell over stdin (wrapped in sudo for a
	// non-root user) so arbitrary snippets — quotes, pipes, &&, redirects — run
	// as root without any quoting gymnastics.
	remote := "bash"
	if s.needsSudo() {
		remote = "sudo -n bash"
	}
	prog, args := s.prog(s.sshArgs(remote))
	c := exec.CommandContext(ctx, prog, args...)
	c.Env = s.passwordEnv()
	c.Stdin = strings.NewReader(cmd)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return stdout.String(), fmt.Errorf("ssh %q: %w: %s", cmd, err, stderr.String())
	}
	return stdout.String(), nil
}

// Put writes content to remotePath atomically with the given mode (the target
// directory must already exist).
func (s SSHRunner) Put(ctx context.Context, content []byte, remotePath string, mode os.FileMode) error {
	tmp := remotePath + ".tmp"
	// %q yields double-quoted paths, so the inner command carries no single
	// quotes and is safe to wrap in a single-quoted `sudo -n sh -c '...'`. The
	// file content still streams in on stdin, which cat reads inside the wrapper.
	inner := fmt.Sprintf("cat > %q && chmod %o %q && mv %q %q", tmp, mode.Perm(), tmp, tmp, remotePath)
	remote := inner
	if s.needsSudo() {
		remote = "sudo -n sh -c '" + inner + "'"
	}
	prog, args := s.prog(s.sshArgs(remote))
	c := exec.CommandContext(ctx, prog, args...)
	c.Env = s.passwordEnv()
	c.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("ssh put %s: %w: %s", remotePath, err, stderr.String())
	}
	return nil
}
