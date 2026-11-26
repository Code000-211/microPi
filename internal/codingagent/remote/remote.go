// Package remote implements SSH ControlMaster routing for /remote and --remote.
package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Target identifies a remote host and working directory.
type Target struct {
	Host string
	Dir  string
}

// ParseTarget parses "host:dir" or "host" into a Target.
func ParseTarget(spec string) (Target, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return Target{}, errors.New("remote target cannot be empty")
	}
	host, dir, hasColon := strings.Cut(spec, ":")
	host = strings.TrimSpace(host)
	if host == "" {
		return Target{}, fmt.Errorf("invalid remote target %q: missing host", spec)
	}
	dir = strings.TrimSpace(dir)
	if !hasColon || dir == "" {
		dir = "."
	}
	return Target{Host: host, Dir: dir}, nil
}

// Manager manages the active SSH remote connection for a session.
type Manager struct {
	mu          sync.RWMutex
	active      bool
	target      Target
	controlPath string
	localCWD    string
}

// NewManager creates a Manager anchored at localCWD.
func NewManager(localCWD string) *Manager {
	return &Manager{localCWD: localCWD}
}

// Active reports whether remote routing is currently enabled.
func (m *Manager) Active() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// Target returns the active remote target and whether remote routing is active.
func (m *Manager) Target() (Target, bool) {
	if m == nil {
		return Target{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.target, m.active
}

// SSHBin returns the ssh executable path (honoring PI_SSH_COMMAND).
func SSHBin() string {
	if v := strings.TrimSpace(os.Getenv("PI_SSH_COMMAND")); v != "" {
		return v
	}
	return "ssh"
}

// Connect parses spec, establishes or verifies the SSH ControlMaster connection, and activates remote routing.
func (m *Manager) Connect(spec string) (Target, error) {
	t, err := ParseTarget(spec)
	if err != nil {
		return Target{}, err
	}
	sock := controlSocketPath(t.Host)
	_ = os.MkdirAll(filepath.Dir(sock), 0o700)

	// Verify connection (also warms ControlMaster if supported).
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := m.sshArgs(t.Host, sock)
	args = append(args, "true")
	//nolint:gosec // G204: ssh executable and host/target are user-configured via /remote or --remote
	cmd := exec.CommandContext(ctx, SSHBin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return Target{}, fmt.Errorf("ssh connect %s: %w (%s)", t.Host, err, msg)
		}
		return Target{}, fmt.Errorf("ssh connect %s: %w", t.Host, err)
	}

	m.mu.Lock()
	m.active = true
	m.target = t
	m.controlPath = sock
	m.mu.Unlock()
	return t, nil
}

// Disconnect closes any active ControlMaster and restores local routing.
func (m *Manager) Disconnect() {
	if m == nil {
		return
	}
	m.mu.Lock()
	active := m.active
	t := m.target
	sock := m.controlPath
	m.active = false
	m.target = Target{}
	m.controlPath = ""
	m.mu.Unlock()

	if active && sock != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		//nolint:gosec // G204: closing ControlMaster socket
		_ = exec.CommandContext(ctx, SSHBin(), "-S", sock, "-O", "exit", t.Host).Run()
	}
}

func controlSocketPath(host string) string {
	sum := sha256.Sum256([]byte(host))
	return filepath.Join(os.TempDir(), "upi-ssh-"+hex.EncodeToString(sum[:8])+".sock")
}

func (m *Manager) sshArgs(host, sock string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + sock,
		"-o", "ControlPersist=600",
		host,
	}
}

// ResolveRemotePath resolves a tool path (which may have been joined to localCWD by toolCWD)
// into a path relative to or inside the remote working directory.
func (m *Manager) ResolveRemotePath(p string) string {
	m.mu.RLock()
	t := m.target
	localCWD := m.localCWD
	m.mu.RUnlock()

	if localCWD != "" {
		if rel, err := filepath.Rel(localCWD, p); err == nil && rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			p = filepath.ToSlash(rel)
		}
	}
	if strings.HasPrefix(p, "/") {
		return path.Clean(p)
	}
	if t.Dir == "" || t.Dir == "." {
		return path.Clean(p)
	}
	return path.Join(t.Dir, p)
}

// ShellQuote quotes a string for POSIX sh/bash.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Exec runs shellCmd inside the remote target directory, streaming stdout+stderr to onData.
func (m *Manager) Exec(ctx context.Context, shellCmd string, stdinData []byte, onData func([]byte)) (int, error) {
	m.mu.RLock()
	active := m.active
	t := m.target
	sock := m.controlPath
	m.mu.RUnlock()
	if !active {
		return 1, errors.New("remote session is not active")
	}

	remoteScript := shellCmd
	if t.Dir != "" && t.Dir != "." {
		remoteScript = "cd " + ShellQuote(t.Dir) + " && " + shellCmd
	}
	args := m.sshArgs(t.Host, sock)
	args = append(args, remoteScript)

	//nolint:gosec // G204: ssh remote command execution
	cmd := exec.CommandContext(ctx, SSHBin(), args...)
	if stdinData != nil {
		cmd.Stdin = bytes.NewReader(stdinData)
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		return 1, err
	}

	var readErr error
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := pr.Read(buf)
			if n > 0 && onData != nil {
				onData(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					readErr = err
				}
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	_ = pw.Close()
	<-readDone
	_ = pr.Close()
	_ = readErr

	if ctx.Err() != nil {
		return 1, ctx.Err()
	}
	if waitErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 1, waitErr
}

// ReadFile reads a file on the remote host.
func (m *Manager) ReadFile(ctx context.Context, targetPath string) ([]byte, error) {
	rp := m.ResolveRemotePath(targetPath)
	var out, errBuf bytes.Buffer
	code, err := m.Exec(ctx, "cat -- "+ShellQuote(rp), nil, func(b []byte) {
		out.Write(b)
	})
	_ = errBuf
	if err != nil {
		return nil, err
	}
	if code != 0 {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = fmt.Sprintf("remote read %s failed with exit code %d", rp, code)
		}
		return nil, &os.PathError{Op: "open", Path: rp, Err: errors.New(msg)}
	}
	return out.Bytes(), nil
}

// Access checks whether targetPath exists and is readable on the remote host.
func (m *Manager) Access(ctx context.Context, targetPath string) error {
	rp := m.ResolveRemotePath(targetPath)
	code, err := m.Exec(ctx, "test -r "+ShellQuote(rp), nil, nil)
	if err != nil {
		return err
	}
	if code != 0 {
		return &os.PathError{Op: "stat", Path: rp, Err: os.ErrNotExist}
	}
	return nil
}

// WriteFile writes data to targetPath on the remote host (creating parent dirs).
func (m *Manager) WriteFile(ctx context.Context, targetPath string, data []byte) error {
	rp := m.ResolveRemotePath(targetPath)
	dir := path.Dir(rp)
	cmd := "mkdir -p -- " + ShellQuote(dir) + " && cat > " + ShellQuote(rp)
	var out bytes.Buffer
	code, err := m.Exec(ctx, cmd, data, func(b []byte) {
		out.Write(b)
	})
	if err != nil {
		return err
	}
	if code != 0 {
		msg := strings.TrimSpace(out.String())
		if msg == "" {
			msg = fmt.Sprintf("remote write %s failed with exit code %d", rp, code)
		}
		return &os.PathError{Op: "write", Path: rp, Err: errors.New(msg)}
	}
	return nil
}

// Mkdir creates a directory on the remote host.
func (m *Manager) Mkdir(ctx context.Context, dir string) error {
	rp := m.ResolveRemotePath(dir)
	code, err := m.Exec(ctx, "mkdir -p -- "+ShellQuote(rp), nil, nil)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("remote mkdir %s failed with exit code %d", rp, code)
	}
	return nil
}
