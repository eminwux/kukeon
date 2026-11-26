// Copyright 2026 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Package exec implements `kuke exec`: run a new process inside a running
// container of a cell, as the container's own user, as root, or as an
// explicit user (issue #1432). kukeond starts the process and passes one end
// of a socket pair back over its own socket; this command frames stdin and
// window resizes onto it and writes the stdout and stderr frames it gets
// back to its own stdout and stderr. The command's exit code is the
// process's.
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/signal"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

const usageLine = "usage: kuke exec <cell> [flags] -- <command> [args...]"

// ExitError is returned when the executed process exited with a non-zero
// code. cmd/main turns it into kuke's own exit code and prints nothing for
// it: the process's stderr already said what went wrong.
type ExitError struct {
	Code int
}

func (e ExitError) Error() string { return fmt.Sprintf("command exited with code %d", e.Code) }

// ExitCode returns the executed process's exit code.
func (e ExitError) ExitCode() int { return e.Code }

// options are the flags that do not go through viper.
type options struct {
	root    bool
	user    string
	stdin   bool
	tty     bool
	env     []string
	workdir string
}

// NewExecCmd builds the `kuke exec` cobra command.
func NewExecCmd() *cobra.Command {
	opts := &options{}
	cmd := &cobra.Command{
		Use:   "exec <cell> [flags] -- <command> [args...]",
		Short: "Run a command in a container of a running cell",
		Long: "Run a new process in a container of a running cell and exit with its exit code.\n\n" +
			"The process runs as the container's own user unless --root (uid 0, gid 0) or\n" +
			"--user is given. Its stdout and stderr arrive on kuke's stdout and stderr. The\n" +
			"process is killed when kuke exits or loses its connection to kukeond.\n\n" +
			"Everything after -- is the command; kuke does not interpret it.",
		Example: "  kuke exec my-cell -- id -u\n" +
			"  kuke exec my-cell --root -- apt-get install -y curl\n" +
			"  kuke exec my-cell -c work -e DEBUG=1 -w /tmp -- ./script.sh\n" +
			"  echo hi | kuke exec my-cell -i -- cat\n" +
			"  kuke exec my-cell -it -- bash",
		Args:         validateArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.run(cmd, args)
		},
	}

	cmd.Flags().String("realm", consts.KukeonDefaultRealmName, "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_EXEC_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", consts.KukeonDefaultSpaceName, "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_EXEC_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", consts.KukeonDefaultStackName, "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_EXEC_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().StringP("container", "c", "",
		"Container within the cell to run in (omit to pick the cell's only non-root container)")
	_ = viper.BindPFlag(config.KUKE_EXEC_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))

	cmd.Flags().BoolVar(&opts.root, "root", false, "Run as root (uid 0, gid 0) inside the container")
	cmd.Flags().StringVarP(&opts.user, "user", "u", "",
		"Run as this user: uid, uid:gid, or a name from the container's /etc/passwd")
	cmd.Flags().BoolVarP(&opts.stdin, "stdin", "i", false, "Forward kuke's stdin to the process")
	cmd.Flags().BoolVarP(&opts.tty, "tty", "t", false, "Allocate a terminal for the process")
	cmd.Flags().StringArrayVarP(&opts.env, "env", "e", nil,
		"Set an environment variable for the process, as KEY=VAL (repeatable)")
	cmd.Flags().StringVarP(&opts.workdir, "workdir", "w", "", "Working directory of the process")
	cmd.MarkFlagsMutuallyExclusive("root", "user")
	// Without --, the command's own flags are parsed as kuke's: point at
	// the -- form whatever the parser made of them.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return fmt.Errorf("%w\n%s", err, usageLine)
	})

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("container", config.CompleteContainerNames)

	return cmd
}

// validateArgs requires exactly one cell before -- and a command after it.
func validateArgs(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	var problem string
	switch {
	case dash < 0:
		problem = "the command must come after --"
	case dash == 0:
		problem = "a cell name is required before --"
	case dash > 1:
		problem = fmt.Sprintf("expected one cell name before --, got %d arguments", dash)
	case len(args) == dash:
		problem = "a command is required after --"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s\n%s", errdefs.ErrExecInvalidRequest, problem, usageLine)
}

func (o *options) run(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	target := kukeshared.TerminalTarget{
		Cell:      args[0],
		Realm:     viper.GetString(config.KUKE_EXEC_REALM.ViperKey),
		Space:     viper.GetString(config.KUKE_EXEC_SPACE.ViperKey),
		Stack:     viper.GetString(config.KUKE_EXEC_STACK.ViperKey),
		Container: viper.GetString(config.KUKE_EXEC_CONTAINER.ViperKey),
	}.Trimmed()
	if err := target.Validate(); err != nil {
		return err
	}
	req, tty, err := o.request(cmd, args[1:])
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	// The same cell-level guard as `kuke attach`, then the container picker.
	doc, err := kukeshared.ResolveExecTarget(ctx, client, target)
	if err != nil {
		return err
	}
	conn, err := client.ExecContainer(ctx, doc, req)
	if err != nil {
		return kukeshared.WrapExecError(err, doc)
	}

	exit, err := o.stream(ctx, cmd, conn, tty)
	switch {
	case err != nil:
		return err
	case exit.Error != "":
		return fmt.Errorf("%w: %s", errdefs.ErrExecFailed, exit.Error)
	case exit.Code != 0:
		// Nothing is printed for the process's own failure.
		cmd.SilenceErrors = true
		return ExitError{Code: exit.Code}
	}
	return nil
}

// request builds the ExecRequest from the flags and, with -t, returns the
// terminal on stdin. Everything it refuses is refused before any RPC.
func (o *options) request(cmd *cobra.Command, command []string) (kukeonv1.ExecRequest, *os.File, error) {
	req := kukeonv1.ExecRequest{
		Command: command,
		Root:    o.root,
		User:    strings.TrimSpace(o.user),
		Env:     o.env,
		Workdir: o.workdir,
		Stdin:   o.stdin,
		TTY:     o.tty,
	}
	for i, entry := range o.env {
		if key, _, ok := strings.Cut(entry, "="); !ok || key == "" {
			return req, nil, fmt.Errorf("%w: --env value %d is not KEY=VAL", errdefs.ErrExecInvalidRequest, i+1)
		}
	}
	if !o.tty {
		return req, nil, nil
	}
	tty, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(tty.Fd())) {
		return req, nil, fmt.Errorf(
			"%w: -t/--tty needs a terminal on stdin; drop -t to run without one (-i alone forwards piped input)",
			errdefs.ErrExecInvalidRequest)
	}
	if cols, rows, sizeErr := terminalSize(tty); sizeErr == nil {
		req.Cols, req.Rows = cols, rows
	}
	return req, tty, nil
}

// stream runs the session on conn until the daemon reports the exit. With
// -it the local terminal is raw for the duration and restored on every way
// out, a dropped connection included.
func (o *options) stream(
	ctx context.Context,
	cmd *cobra.Command,
	conn net.Conn,
	tty *os.File,
) (execstream.Exit, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() { _ = conn.Close() }()

	if tty != nil && o.stdin {
		state, err := term.MakeRaw(int(tty.Fd()))
		if err != nil {
			return execstream.Exit{}, fmt.Errorf("put the terminal in raw mode: %w", err)
		}
		defer func() { _ = term.Restore(int(tty.Fd()), state) }()
		// A raw terminal delivers no keyboard signals; one sent from
		// outside must still end the session through the restore above.
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, unix.SIGINT, unix.SIGTERM, unix.SIGHUP)
		defer stop()
	}
	// Closing the connection is what unblocks the receive loop.
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	w := execstream.NewWriter(conn)
	if o.stdin {
		go forwardStdin(cmd.InOrStdin(), w)
	}
	if tty != nil {
		go forwardResizes(ctx, tty, w)
	}
	return execstream.Receive(conn, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// forwardStdin frames in onto the connection and reports its end, so the
// process reads EOF. A read error ends stdin the same way.
func forwardStdin(in io.Reader, w *execstream.Writer) {
	buf := make([]byte, execstream.MaxPayload)
	for {
		n, err := in.Read(buf)
		if n > 0 && w.WriteData(execstream.TypeStdin, buf[:n]) != nil {
			return
		}
		if err != nil {
			_ = w.StdinEOF()
			return
		}
	}
}

// forwardResizes sends the terminal's size on every SIGWINCH.
func forwardResizes(ctx context.Context, tty *os.File, w *execstream.Writer) {
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, unix.SIGWINCH)
	defer signal.Stop(winch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-winch:
			cols, rows, err := terminalSize(tty)
			if err != nil {
				continue
			}
			if w.Resize(cols, rows) != nil {
				return
			}
		}
	}
}

func terminalSize(tty *os.File) (uint16, uint16, error) {
	width, height, err := term.GetSize(int(tty.Fd()))
	if err != nil {
		return 0, 0, err
	}
	if width <= 0 || height <= 0 || width > math.MaxUint16 || height > math.MaxUint16 {
		return 0, 0, errors.New("terminal size out of range")
	}
	return uint16(width), uint16(height), nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
