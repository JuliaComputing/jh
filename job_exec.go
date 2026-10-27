package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type remoteExitError struct{ code int }

func (e *remoteExitError) Error() string {
	return fmt.Sprintf("remote command exited with code %d", e.code)
}

type shellClient struct {
	base, token string
	http        *http.Client
}

func appURL(job *JobDetails) (string, error) {
	if job.Status != "Running" {
		return "", fmt.Errorf("job %q is not running (status %s)", job.JobName, job.Status)
	}
	if job.ProxyLink == nil || *job.ProxyLink == "" {
		return "", errors.New("job has no app link; it must run an agentshell image with an app port")
	}
	return validateAppURL(*job.ProxyLink)
}

func validateAppURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("app URL must be a full http(s) URL without credentials, query or fragment; use --url to override")
	}
	if u.Scheme == "http" && !localAppURL(u) {
		return "", errors.New("refusing insecure non-local app URL; use HTTPS")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func localAppURL(u *url.URL) bool {
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

func shellTarget(cmd *cobra.Command, job string) (*shellClient, error) {
	override, _ := cmd.Flags().GetString("url")
	c := &shellClient{http: &http.Client{Timeout: 75 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	c.http.Transport = transport
	if override != "" {
		base, err := validateAppURL(override)
		if err != nil {
			return nil, err
		}
		c.base = base
		u, _ := url.Parse(base)
		if localAppURL(u) {
			return c, nil
		}
	}
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}
	c.token = token.IDToken
	if override == "" {
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			return nil, err
		}
		job, err := fetchJobDetails(c.http, jobBaseURL(server), c.token, job)
		if err != nil {
			return nil, err
		}
		c.base, err = appURL(job)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (c *shellClient) headers() http.Header {
	h := make(http.Header)
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
	return h
}

func (c *shellClient) json(ctx context.Context, method, path string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header = c.headers()
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("agentshell returned %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func shellQuote(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
	}
	return strings.Join(quoted, " ")
}

func demuxOutput(frame []byte, stdout, stderr io.Writer) error {
	if len(frame) < 1 {
		return errors.New("empty output frame")
	}
	var w io.Writer
	switch frame[0] {
	case 1:
		w = stdout
	case 2:
		w = stderr
	default:
		return fmt.Errorf("invalid stream id %d", frame[0])
	}
	_, err := w.Write(frame[1:])
	return err
}

func (c *shellClient) attach(ctx context.Context, id string, stdin io.Reader, stdout, stderr io.Writer, resize <-chan [2]int) error {
	u, _ := url.Parse(c.base + "/commands/" + url.PathEscape(id) + "/attach")
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wsHTTP := *c.http
	wsHTTP.Timeout = 0
	ws, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: c.headers(), HTTPClient: &wsHTTP})
	if err != nil {
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(2 << 20)
	errorsCh := make(chan error, 2)
	write := func(typ websocket.MessageType, data []byte) error {
		wctx, done := context.WithTimeout(ctx, 10*time.Second)
		defer done()
		return ws.Write(wctx, typ, data)
	}
	go func() {
		buf := make([]byte, 32768)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				if e := write(websocket.MessageBinary, buf[:n]); e != nil {
					errorsCh <- e
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					err = write(websocket.MessageText, []byte(`{"type":"stdin_close"}`))
				}
				if err != nil {
					errorsCh <- err
					cancel()
				}
				return
			}
		}
	}()
	go func() {
		for {
			select {
			case size := <-resize:
				data, _ := json.Marshal(map[string]any{"type": "resize", "rows": size[0], "cols": size[1]})
				if err := write(websocket.MessageText, data); err != nil {
					errorsCh <- err
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			select {
			case e := <-errorsCh:
				return e
			default:
				return fmt.Errorf("attach closed before exit status: %w", err)
			}
		}
		if typ == websocket.MessageBinary {
			if err := demuxOutput(data, stdout, stderr); err != nil {
				return err
			}
			continue
		}
		var exit struct {
			Type   string `json:"type"`
			Code   *int   `json:"exit_code"`
			State  string `json:"state"`
			Signal string `json:"signal"`
		}
		if err := json.Unmarshal(data, &exit); err != nil {
			return err
		}
		if exit.Type != "exit" {
			return fmt.Errorf("unknown control frame %q", exit.Type)
		}
		_ = ws.Close(websocket.StatusNormalClosure, "")
		if exit.Code == nil {
			return fmt.Errorf("remote command %s (no exit code)", exit.State)
		}
		code := *exit.Code
		if code < 0 {
			code = 1
			switch exit.Signal {
			case "interrupt":
				code = 130
			case "terminated":
				code = 143
			case "killed":
				code = 137
			}
		}
		if code != 0 {
			return &remoteExitError{code}
		}
		return nil
	}
}

func newJobExecCmd() *cobra.Command {
	var forceTTY, noTTY bool
	var cwd, override string
	var env []string
	var timeout time.Duration
	cmd := &cobra.Command{Use: "exec <job-name> [flags] -- <command...>", Short: "Execute a command in an agentshell job", Args: cobra.MinimumNArgs(1), SilenceErrors: true, SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if forceTTY && noTTY {
			return errors.New("--tty and --no-tty are mutually exclusive")
		}
		if timeout < 0 || timeout > 7*24*time.Hour {
			return errors.New("--timeout must be between 0 and 168h")
		}
		envs := map[string]string{}
		for _, item := range env {
			k, v, ok := strings.Cut(item, "=")
			if !ok || k == "" {
				return fmt.Errorf("invalid --env %q; expected K=V", item)
			}
			envs[k] = v
		}
		in, inOK := cmd.InOrStdin().(*os.File)
		out, outOK := cmd.OutOrStdout().(*os.File)
		tty := !noTTY && (forceTTY || (inOK && outOK && term.IsTerminal(int(in.Fd())) && term.IsTerminal(int(out.Fd()))))
		body := map[string]any{"command": "julia", "cwd": cwd, "env": envs, "timeout_seconds": int(math.Ceil(timeout.Seconds())), "tty": tty, "stdin": true}
		if len(args) > 1 {
			body["command"] = shellQuote(args[1:])
		}
		var resize <-chan [2]int
		stop := func() {}
		if tty && inOK && term.IsTerminal(int(in.Fd())) {
			cols, rows, err := term.GetSize(int(in.Fd()))
			if err != nil {
				return err
			}
			body["rows"] = rows
			body["cols"] = cols
		}
		client, err := shellTarget(cmd, args[0])
		if err != nil {
			return err
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := client.json(cmd.Context(), "POST", "/commands", body, &created); err != nil {
			return err
		}
		if created.ID == "" {
			return errors.New("agentshell returned no command id")
		}
		if tty && inOK && term.IsTerminal(int(in.Fd())) {
			state, err := term.MakeRaw(int(in.Fd()))
			if err != nil {
				return err
			}
			defer term.Restore(int(in.Fd()), state)
			resize, stop = watchTerminalSize(int(in.Fd()))
		}
		defer stop()
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return client.attach(ctx, created.ID, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), resize)
	}
	cmd.Flags().StringVar(&override, "url", "", "Override agentshell app URL (localhost HTTP needs no login)")
	cmd.Flags().BoolVarP(&forceTTY, "tty", "t", false, "Request a remote PTY")
	cmd.Flags().BoolVarP(&noTTY, "no-tty", "T", false, "Disable PTY; preserve separate stdout and stderr")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Remote working directory")
	cmd.Flags().StringArrayVar(&env, "env", nil, "Remote environment K=V (repeatable)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Remote timeout, e.g. 30s or 5m (0 unlimited)")
	return cmd
}

func newJobCommandsCmd() *cobra.Command {
	var override string
	var asJSON bool
	cmd := &cobra.Command{Use: "commands <job-name>", Short: "List recent agentshell commands", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := shellTarget(cmd, args[0])
		if err != nil {
			return err
		}
		var items []json.RawMessage
		if err := client.json(cmd.Context(), "GET", "/commands", nil, &items); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(items)
		}
		for _, item := range items {
			var c struct {
				ID, State, Command string
				ExitCode           *int `json:"exit_code"`
			}
			if err := json.Unmarshal(item, &c); err != nil {
				return err
			}
			code := "-"
			if c.ExitCode != nil {
				code = fmt.Sprint(*c.ExitCode)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\n", c.ID, c.State, code, strings.ReplaceAll(c.Command, "\n", "\\n"))
		}
		return nil
	}}
	cmd.Flags().StringVar(&override, "url", "", "Override agentshell app URL")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print JSON")
	return cmd
}

func newJobOutputCmd() *cobra.Command {
	var override string
	var follow bool
	cmd := &cobra.Command{Use: "output <job-name> <command-id>", Short: "Read persisted agentshell output", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		client, err := shellTarget(cmd, args[0])
		if err != nil {
			return err
		}
		offset := int64(0)
		for {
			wait := 0
			if follow {
				wait = 60
			}
			var out struct {
				Data string
				Next int64 `json:"next_offset"`
				EOF  bool  `json:"eof"`
			}
			path := fmt.Sprintf("/commands/%s/output?offset=%d&wait=%d", url.PathEscape(args[1]), offset, wait)
			if err := client.json(cmd.Context(), "GET", path, nil, &out); err != nil {
				return err
			}
			if _, err := io.WriteString(cmd.OutOrStdout(), out.Data); err != nil {
				return err
			}
			if out.Next < offset {
				return errors.New("invalid output offset from server")
			}
			if out.EOF || (!follow && out.Next == offset) {
				return nil
			}
			offset = out.Next
		}
	}}
	cmd.Flags().StringVar(&override, "url", "", "Override agentshell app URL")
	cmd.Flags().BoolVar(&follow, "follow", false, "Long-poll until the command finishes")
	return cmd
}
