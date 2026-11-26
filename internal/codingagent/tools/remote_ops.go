package tools

import (
	"context"
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent/remote"
)

// RemoteReadOperations routes read operations to remoteMgr when active, otherwise to local disk.
func RemoteReadOperations(remoteMgr *remote.Manager, base *ReadOperations) *ReadOperations {
	if remoteMgr == nil {
		return base
	}
	return &ReadOperations{
		ReadFile: func(p string) ([]byte, error) {
			if remoteMgr.Active() {
				return remoteMgr.ReadFile(context.Background(), p)
			}
			if base != nil && base.ReadFile != nil {
				return base.ReadFile(p)
			}
			return os.ReadFile(p)
		},
		Access: func(p string) error {
			if remoteMgr.Active() {
				return remoteMgr.Access(context.Background(), p)
			}
			if base != nil && base.Access != nil {
				return base.Access(p)
			}
			_, err := os.Stat(p)
			return err
		},
		DetectImageMimeType: func(p string) (string, error) {
			if remoteMgr.Active() {
				return "", nil
			}
			if base != nil && base.DetectImageMimeType != nil {
				return base.DetectImageMimeType(p)
			}
			return "", nil
		},
	}
}

// RemoteWriteOperations routes write operations to remoteMgr when active, otherwise to local disk.
func RemoteWriteOperations(remoteMgr *remote.Manager, base *WriteOperations) *WriteOperations {
	if remoteMgr == nil {
		return base
	}
	return &WriteOperations{
		WriteFile: func(p, content string) error {
			if remoteMgr.Active() {
				return remoteMgr.WriteFile(context.Background(), p, []byte(content))
			}
			if base != nil && base.WriteFile != nil {
				return base.WriteFile(p, content)
			}
			return os.WriteFile(p, []byte(content), 0o644)
		},
		Mkdir: func(dir string) error {
			if remoteMgr.Active() {
				return remoteMgr.Mkdir(context.Background(), dir)
			}
			if base != nil && base.Mkdir != nil {
				return base.Mkdir(dir)
			}
			return os.MkdirAll(dir, 0o755)
		},
		ReadExisting: func(p string) (string, bool, error) {
			if remoteMgr.Active() {
				b, err := remoteMgr.ReadFile(context.Background(), p)
				if err != nil {
					return "", false, nil
				}
				return string(b), true, nil
			}
			if base != nil && base.ReadExisting != nil {
				return base.ReadExisting(p)
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return "", false, nil
			}
			return string(b), true, nil
		},
	}
}

// RemoteEditOperations routes edit operations to remoteMgr when active, otherwise to local disk.
func RemoteEditOperations(remoteMgr *remote.Manager, base *EditOperations) *EditOperations {
	if remoteMgr == nil {
		return base
	}
	return &EditOperations{
		ReadFile: func(p string) ([]byte, error) {
			if remoteMgr.Active() {
				return remoteMgr.ReadFile(context.Background(), p)
			}
			if base != nil && base.ReadFile != nil {
				return base.ReadFile(p)
			}
			return os.ReadFile(p)
		},
		WriteFile: func(p, content string) error {
			if remoteMgr.Active() {
				return remoteMgr.WriteFile(context.Background(), p, []byte(content))
			}
			if base != nil && base.WriteFile != nil {
				return base.WriteFile(p, content)
			}
			return os.WriteFile(p, []byte(content), 0o644)
		},
		Access: func(p string) error {
			if remoteMgr.Active() {
				return remoteMgr.Access(context.Background(), p)
			}
			if base != nil && base.Access != nil {
				return base.Access(p)
			}
			_, err := os.Stat(p)
			return err
		},
	}
}

// RemoteBashOperations routes bash execution to remoteMgr when active, otherwise to localOps.
type RemoteBashOperations struct {
	Remote *remote.Manager
	Local  BashOperations
}

// Exec executes command remotely if Remote.Active(), else locally via Local.
func (o *RemoteBashOperations) Exec(ctx context.Context, command, cwd string, opts BashOperationsExecOptions) (BashOperationsResult, error) {
	if o.Remote != nil && o.Remote.Active() {
		code, err := o.Remote.Exec(ctx, command, nil, opts.OnData)
		if err != nil {
			if ctx.Err() != nil {
				return BashOperationsResult{}, errBashAborted
			}
			return BashOperationsResult{}, err
		}
		return BashOperationsResult{ExitCode: &code}, nil
	}
	return o.Local.Exec(ctx, command, cwd, opts)
}
