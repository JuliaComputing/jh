package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

type shellOptions struct {
	product, node, image, tag, alias, project string
	env                                       []string
	limit                                     time.Duration
	port                                      int
	keep                                      bool
	wait                                      time.Duration
}

func (o shellOptions) multipart() bool {
	return o.node != "" || o.image != "" || o.tag != "" || o.alias != "" || len(o.env) > 0 || o.limit > 0 || o.port > 0
}

func (o shellOptions) validate() error {
	if o.product == "" {
		return errors.New("--product must not be empty")
	}
	if o.multipart() && o.node == "" {
		return errors.New("--node is required with job overrides (--env, --alias, --image-tag, --image-name, --timeout or --port); jobmanagement requires an explicit node for multipart submission")
	}
	if o.limit < 0 {
		return errors.New("invalid job --timeout")
	}
	if o.wait <= 0 {
		return errors.New("--wait-timeout must be positive")
	}
	if o.port < 0 || o.port > 32767 {
		return errors.New("--port must be 1..32767 (or 0 for product default)")
	}
	for _, env := range o.env {
		key, _, ok := strings.Cut(env, "=")
		if !ok || key == "" {
			return fmt.Errorf("invalid --env %q; expected K=V", env)
		}
	}
	return nil
}

func (c *shellClient) submitShell(ctx context.Context, o shellOptions) (string, error) {
	var data struct {
		Data struct {
			Job string `json:"job_name"`
		} `json:"data"`
	}
	if !o.multipart() {
		body := map[string]string{"product_name": o.product}
		if o.project != "" {
			body["project_id"] = o.project
		}
		if err := c.json(ctx, "POST", "/api/v1/jobs/app_default", body, &data); err != nil {
			return "", err
		}
	} else {
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		fields := map[string]string{"product_name": o.product, "node": o.node, "node_count": "1", "node_process_per": "1", "worker_pool_type": "fixed", "interactive_authentication": "me", "limit_type": "time", "limit_value": "2"}
		for key, value := range map[string]string{"image_name": o.image, "image_tag": o.tag, "alias": o.alias, "project_id": o.project} {
			if value != "" {
				fields[key] = value
			}
		}
		if o.limit > 0 {
			fields["limit_value"] = strconv.Itoa(int(math.Ceil(o.limit.Hours())))
		}
		if o.port > 0 {
			fields["interactive_port"] = strconv.Itoa(o.port)
		}
		envs := map[string]string{}
		for _, item := range o.env {
			k, v, _ := strings.Cut(item, "=")
			envs[k] = v
		}
		encoded, _ := json.Marshal(envs)
		fields["user_env"] = string(encoded)
		for key, value := range fields {
			if err := form.WriteField(key, value); err != nil {
				return "", err
			}
		}
		if err := form.Close(); err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, "POST", c.base+"/api/v1/jobs", &buf)
		if err != nil {
			return "", err
		}
		req.Header = c.headers()
		req.Header.Set("Content-Type", form.FormDataContentType())
		resp, err := c.http.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return "", fmt.Errorf("job submission returned %d: %s", resp.StatusCode, body)
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
			return "", err
		}
	}
	if data.Data.Job == "" {
		return "", errors.New("submission returned no data.job_name")
	}
	return data.Data.Job, nil
}

func pauseShell(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *shellClient) waitShell(ctx context.Context, job string, interval time.Duration, status io.Writer) (string, error) {
	last := ""
	waiting := false
	for {
		var details JobDetails
		if err := c.json(ctx, "GET", "/api/v1/jobs/"+url.PathEscape(job), nil, &details); err != nil {
			return "", err
		}
		if details.Status != last {
			fmt.Fprintf(status, "Job %s: %s\n", job, details.Status)
			last = details.Status
		}
		switch details.Status {
		case "Completed", "Failed", "Error", "Killed", "Stopped", "SubmitFailed":
			return "", fmt.Errorf("job %s ended before attachment (%s)", job, details.Status)
		}
		if details.Status == "Running" && details.ProxyLink != nil && *details.ProxyLink != "" {
			base, err := appURL(&details)
			if err != nil {
				return "", err
			}
			if !waiting {
				fmt.Fprintln(status, "Waiting for agentshell API...")
				waiting = true
			}
			req, err := http.NewRequestWithContext(ctx, "GET", base+"/healthz", nil)
			if err != nil {
				return "", err
			}
			req.Header = c.headers()
			resp, err := c.http.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == 200 {
					return base, nil
				}
				if resp.StatusCode == 401 || resp.StatusCode == 403 {
					return "", fmt.Errorf("agentshell health check unauthorized (status %d)", resp.StatusCode)
				}
			}
		}
		if err := pauseShell(ctx, interval); err != nil {
			return "", err
		}
	}
}

func (c *shellClient) runShell(ctx context.Context, o shellOptions, interval time.Duration, status io.Writer, attach func(context.Context, string, string) error) (result error) {
	if err := o.validate(); err != nil {
		return err
	}
	fmt.Fprintf(status, "Submitting %s job...\n", o.product)
	job, err := c.submitShell(ctx, o)
	if err != nil {
		return err
	}
	fmt.Fprintf(status, "Job: %s\n", job)
	defer func() {
		if o.keep {
			fmt.Fprintf(status, "Keeping job %s; reattach with: jh job exec %s\n", job, job)
			return
		}
		fmt.Fprintf(status, "Stopping job %s...\n", job)
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var response any
		if err := c.json(cleanup, "DELETE", "/api/v1/jobs/"+url.PathEscape(job), nil, &response); err != nil {
			fmt.Fprintf(status, "Cleanup failed for %s: %v; stop it in JuliaHub to avoid charges.\n", job, err)
			if result == nil {
				result = err
			}
		}
	}()
	startup, cancel := context.WithTimeout(ctx, o.wait)
	base, err := c.waitShell(startup, job, interval, status)
	cancel()
	if err != nil {
		return err
	}
	return attach(ctx, job, base)
}

func newJobShellCmd() *cobra.Command {
	o := shellOptions{}
	var tty, noTTY bool
	var cwd string
	cmd := &cobra.Command{Use: "shell [flags] [-- command...]", Short: "Start an agentshell job, attach, then stop it", SilenceErrors: true, SilenceUsage: true}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if tty && noTTY {
			return errors.New("--tty and --no-tty are mutually exclusive")
		}
		if err := o.validate(); err != nil {
			return err
		}
		server, err := getServerFromFlagOrConfig(cmd)
		if err != nil {
			return err
		}
		token, err := ensureValidToken()
		if err != nil {
			return fmt.Errorf("authentication required: %w", err)
		}
		client := &shellClient{base: jobBaseURL(server), token: token.IDToken, http: &http.Client{Timeout: 75 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return client.runShell(ctx, o, 2*time.Second, cmd.ErrOrStderr(), func(ctx context.Context, job, base string) error {
			exec := jobExecCommand(&shellClient{base: base, token: token.IDToken, http: client.http})
			exec.SetContext(ctx)
			exec.SetIn(cmd.InOrStdin())
			exec.SetOut(cmd.OutOrStdout())
			exec.SetErr(cmd.ErrOrStderr())
			execArgs := []string{job, "--cwd", cwd}
			if tty {
				execArgs = append(execArgs, "-t")
			}
			if noTTY {
				execArgs = append(execArgs, "-T")
			}
			if len(args) > 0 {
				execArgs = append(execArgs, "--")
				execArgs = append(execArgs, args...)
			}
			exec.SetArgs(execArgs)
			return exec.Execute()
		})
	}
	cmd.Flags().StringVar(&o.product, "product", "agent-shell", "Interactive product to submit")
	cmd.Flags().StringVar(&o.node, "node", "", "Node name (required for job overrides)")
	cmd.Flags().StringVar(&o.image, "image-name", "", "Product image key override")
	cmd.Flags().StringVar(&o.tag, "image-tag", "", "Image tag override")
	cmd.Flags().StringVar(&o.alias, "alias", "", "Job display alias")
	cmd.Flags().StringVar(&o.project, "project-id", "", "Project UUID")
	cmd.Flags().StringArrayVar(&o.env, "env", nil, "Job environment K=V (repeatable)")
	cmd.Flags().DurationVar(&o.limit, "timeout", 0, "Job lifetime, rounded up to whole hours (default: product/user defaults, or 2h with overrides)")
	cmd.Flags().DurationVar(&o.wait, "wait-timeout", 10*time.Minute, "Maximum startup wait")
	cmd.Flags().IntVar(&o.port, "port", 0, "Interactive port (0 uses product default)")
	cmd.Flags().BoolVar(&o.keep, "keep", false, "Keep job after exit or startup cancellation")
	cmd.Flags().BoolVarP(&tty, "tty", "t", false, "Force PTY")
	cmd.Flags().BoolVarP(&noTTY, "no-tty", "T", false, "Disable PTY")
	cmd.Flags().StringVar(&cwd, "cwd", "", "Remote command working directory")
	return cmd
}
