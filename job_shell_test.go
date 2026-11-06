package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestShellLifecycle(t *testing.T) {
	var mu sync.Mutex
	var steps []string
	polls, health := 0, 0
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer", r.URL)
		}
		mu.Lock()
		steps = append(steps, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/jobs/app_default":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["product_name"] != "agent-shell" || body["project_id"] != "project" {
				t.Error(body)
			}
			io.WriteString(w, `{"data":{"job_name":"jr-test"}}`)
		case "/api/v1/jobs/jr-test":
			if r.Method == "DELETE" {
				io.WriteString(w, `{"message":"stopped"}`)
				return
			}
			polls++
			if polls == 1 {
				io.WriteString(w, `{"status":"Submitted"}`)
			} else {
				fmt.Fprintf(w, `{"jobname":"jr-test","status":"Running","proxy_link":%q}`, srv.URL)
			}
		case "/healthz":
			health++
			if health == 1 {
				http.Error(w, "starting", 502)
			} else {
				io.WriteString(w, `{"status":"ok"}`)
			}
		case "/commands":
			io.WriteString(w, `{"id":"c1"}`)
		case "/commands/c1/attach":
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer ws.CloseNow()
			ws.Read(r.Context())
			ws.Write(r.Context(), websocket.MessageBinary, []byte("\x01result\n"))
			ws.Write(r.Context(), websocket.MessageText, []byte(`{"type":"exit","exit_code":4,"signal":"","state":"exited"}`))
			ws.Close(websocket.StatusNormalClosure, "")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &shellClient{base: srv.URL, token: "token", http: srv.Client()}
	var status, output bytes.Buffer
	err := c.runShell(context.Background(), shellOptions{product: "agent-shell", project: "project", wait: time.Second}, time.Millisecond, &status, func(ctx context.Context, job, base string) error {
		cmd := jobExecCommand(&shellClient{base: base, token: c.token, http: c.http})
		cmd.SetContext(ctx)
		cmd.SetIn(strings.NewReader(""))
		cmd.SetOut(&output)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{job, "-T", "--", "true"})
		return cmd.Execute()
	})
	var remote *remoteExitError
	if !errors.As(err, &remote) || remote.code != 4 {
		t.Fatal(err)
	}
	if output.String() != "result\n" || !strings.Contains(status.String(), "Stopping job jr-test") {
		t.Fatal(output.String(), status.String())
	}
	mu.Lock()
	defer mu.Unlock()
	if steps[0] != "POST /api/v1/jobs/app_default" || steps[len(steps)-1] != "DELETE /api/v1/jobs/jr-test" || polls < 3 || health != 2 {
		t.Fatal(steps, polls, health)
	}
}

func TestShellMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs" {
			t.Error(r.URL)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer r.MultipartForm.RemoveAll()
		for k, v := range map[string]string{"product_name": "agent-shell", "node": "node1", "image_name": "agentshell", "image_tag": "test", "alias": "test job", "limit_type": "time", "limit_value": "2", "interactive_port": "8080", "interactive_authentication": "me", "project_id": "p"} {
			if r.FormValue(k) != v {
				t.Errorf("%s=%q want %q", k, r.FormValue(k), v)
			}
		}
		var env map[string]string
		if err := json.Unmarshal([]byte(r.FormValue("user_env")), &env); err != nil || env["KEY"] != "a=b" {
			t.Error(env, err)
		}
		io.WriteString(w, `{"data":{"job_name":"jr-test"}}`)
	}))
	defer srv.Close()
	c := shellClient{base: srv.URL, http: srv.Client()}
	opts := shellOptions{product: "agent-shell", node: "node1", image: "agentshell", tag: "test", alias: "test job", project: "p", limit: 90 * time.Minute, port: 8080, env: []string{"KEY=a=b"}, wait: time.Second}
	if err := opts.validate(); err != nil {
		t.Fatal(err)
	}
	job, err := c.submitShell(context.Background(), opts)
	if err != nil || job != "jr-test" {
		t.Fatal(job, err)
	}
}

func TestShellCleanupAndKeep(t *testing.T) {
	for _, mode := range []string{"cancel", "keep", "failed", "health-auth", "attach-failed", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			deleted := false
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST":
					io.WriteString(w, `{"data":{"job_name":"jr"}}`)
				case r.Method == "DELETE":
					deleted = true
					io.WriteString(w, `{}`)
				case r.URL.Path == "/healthz":
					if mode == "health-auth" {
						w.WriteHeader(403)
					} else {
						w.WriteHeader(200)
					}
				default:
					switch mode {
					case "cancel":
						cancel()
						io.WriteString(w, `{"status":"Submitted"}`)
					case "timeout":
						io.WriteString(w, `{"status":"Submitted"}`)
					case "failed":
						io.WriteString(w, `{"status":"SubmitFailed"}`)
					default:
						fmt.Fprintf(w, `{"jobname":"jr","status":"Running","proxy_link":%q}`, srv.URL)
					}
				}
			}))
			defer srv.Close()
			var status bytes.Buffer
			c := shellClient{base: srv.URL, http: srv.Client()}
			err := c.runShell(ctx, shellOptions{product: "agent-shell", wait: 20 * time.Millisecond, keep: mode == "keep"}, time.Millisecond, &status, func(context.Context, string, string) error {
				if mode == "attach-failed" {
					return errors.New("attach failed")
				}
				return nil
			})
			if mode == "keep" {
				if deleted || err != nil || !strings.Contains(status.String(), "jh job exec jr") {
					t.Fatal(deleted, err, status.String())
				}
			} else if !deleted || err == nil {
				t.Fatal(deleted, err)
			}
		})
	}
}

func TestShellSubmitErrors(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":{}}`, `not json`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		c := shellClient{base: srv.URL, http: srv.Client()}
		if _, err := c.submitShell(context.Background(), shellOptions{product: "agent-shell"}); err == nil {
			t.Fatal(body)
		}
		srv.Close()
	}
	for _, o := range []shellOptions{{}, {product: "agent-shell", wait: time.Second, env: []string{"A=B"}}, {product: "agent-shell", wait: time.Second, node: "n", env: []string{"BAD"}}, {product: "agent-shell", wait: time.Second, limit: -time.Second}} {
		if err := o.validate(); err == nil {
			t.Fatal(o)
		}
	}
}
