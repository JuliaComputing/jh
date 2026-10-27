package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
)

func TestAppURL(t *testing.T) {
	for _, tc := range []struct {
		status, link, want string
		bad                bool
	}{
		{"Running", "https://abc.apps.example.com/", "https://abc.apps.example.com", false},
		{"Completed", "https://abc.apps.example.com/", "", true},
		{"Running", "", "", true}, {"Running", "abc", "", true},
		{"Running", "http://remote.example.com", "", true},
		{"Running", "https://user:password@host", "", true},
		{"Running", "http://localhost:8080/", "http://localhost:8080", false},
	} {
		t.Run(tc.status+tc.link, func(t *testing.T) {
			got, err := appURL(&JobDetails{Status: tc.status, ProxyLink: &tc.link})
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
	if _, err := appURL(&JobDetails{Status: "Running"}); err == nil {
		t.Fatal("missing link accepted")
	}
}

func TestFetchAppLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/rest/jobs/jr-test" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.URL.Path, r.Header)
		}
		io.WriteString(w, `{"details":[{"jobname":"jr-test","status":"Running","proxy_link":"https://link.apps.example.com/"}]}`)
	}))
	defer srv.Close()
	job, err := fetchJobDetails(srv.Client(), srv.URL, "token", "jr-test")
	if err != nil {
		t.Fatal(err)
	}
	got, err := appURL(job)
	if err != nil || got != "https://link.apps.example.com" {
		t.Fatal(got, err)
	}
}

func TestExecDefaultAndTTY(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("COLORTERM", "truecolor")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/commands" {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["command"] != "julia" || body["tty"] != true {
				t.Error(body)
			}
			env := body["env"].(map[string]any)
			if env["TERM"] != "xterm-256color" || env["COLORTERM"] != "truecolor" {
				t.Error(env)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"id":"c"}`)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ws.Read(r.Context())
		ws.Write(r.Context(), websocket.MessageText, []byte(`{"type":"exit","exit_code":0,"state":"exited"}`))
		ws.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()
	cmd := newJobExecCmd()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--url", srv.URL, "x", "-t"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestForwardTerminalEnv(t *testing.T) {
	for _, tc := range []struct {
		name, term, color string
		override          bool
	}{
		{"local", "xterm-256color", "truecolor", false},
		{"empty", "", "", false},
		{"override", "xterm-256color", "truecolor", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TERM", tc.term)
			t.Setenv("COLORTERM", tc.color)
			env := map[string]string{}
			if tc.override {
				env["TERM"], env["COLORTERM"] = "vt100", ""
			}
			forwardTerminalEnv(env)
			if tc.override {
				if env["TERM"] != "vt100" || env["COLORTERM"] != "" {
					t.Fatal(env)
				}
			} else if env["TERM"] != tc.term || env["COLORTERM"] != tc.color || (tc.term == "" && len(env) != 0) {
				t.Fatal(env)
			}
		})
	}
}

func TestDemuxOutput(t *testing.T) {
	var out, errout bytes.Buffer
	for _, frame := range [][]byte{{1, 'o'}, {2, 'e'}, {1, 0xff}, {1}} {
		if err := demuxOutput(frame, &out, &errout); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(out.Bytes(), []byte{'o', 0xff}) || errout.String() != "e" {
		t.Fatal(out.Bytes(), errout.String())
	}
	for _, frame := range [][]byte{nil, {3, 'x'}} {
		if err := demuxOutput(frame, &out, &errout); err == nil {
			t.Fatal("bad frame accepted")
		}
	}
}

func TestJobExecProtocol(t *testing.T) {
	var created map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("local override sent credentials")
		}
		switch r.URL.Path {
		case "/commands":
			if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
				t.Error(err)
			}
			w.WriteHeader(201)
			io.WriteString(w, `{"id":"c1"}`)
		case "/commands/c1/attach":
			ws, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer ws.CloseNow()
			typ, data, err := ws.Read(r.Context())
			if err != nil || typ != websocket.MessageBinary || string(data) != "input\n" {
				t.Errorf("stdin %q %v", data, err)
				return
			}
			_, data, err = ws.Read(r.Context())
			if err != nil || !bytes.Contains(data, []byte("stdin_close")) {
				t.Errorf("close %q %v", data, err)
				return
			}
			for _, msg := range []struct {
				typ  websocket.MessageType
				data string
			}{{websocket.MessageBinary, "\x01out\n"}, {websocket.MessageBinary, "\x02err\n"}, {websocket.MessageText, `{"type":"exit","exit_code":4,"state":"exited","signal":""}`}} {
				if err := ws.Write(r.Context(), msg.typ, []byte(msg.data)); err != nil {
					t.Error(err)
					return
				}
			}
			ws.Close(websocket.StatusNormalClosure, "")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cmd := newJobExecCmd()
	var out, errout bytes.Buffer
	cmd.SetIn(strings.NewReader("input\n"))
	cmd.SetOut(&out)
	cmd.SetErr(&errout)
	cmd.SetArgs([]string{"--url", srv.URL, "x", "-T", "--env", "A=a=b", "--cwd", "/work", "--timeout", "1500ms", "--", "sh", "-c", "echo 'quote'"})
	err := cmd.Execute()
	var remote *remoteExitError
	if !errors.As(err, &remote) || remote.code != 4 {
		t.Fatalf("exit %v", err)
	}
	if out.String() != "out\n" || errout.String() != "err\n" {
		t.Fatal(out.String(), errout.String())
	}
	if created["tty"] != false || created["stdin"] != true || created["timeout_seconds"] != float64(2) || created["cwd"] != "/work" || created["command"] != shellQuote([]string{"sh", "-c", "echo 'quote'"}) {
		t.Fatal(created)
	}
	if created["env"].(map[string]any)["A"] != "a=b" {
		t.Fatal(created)
	}
}

func TestAttachAuthAndMissingExit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Error("missing auth")
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ws.Close(websocket.StatusNormalClosure, "")
	}))
	defer srv.Close()
	c := shellClient{base: srv.URL, token: "tok", http: srv.Client()}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	err := c.attach(context.Background(), "c", reader, io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "before exit") {
		t.Fatal(err)
	}
}

func TestJobCommandsAndOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/commands" {
			io.WriteString(w, `[{"id":"c","state":"exited","command":"true","exit_code":0}]`)
			return
		}
		if r.URL.Query().Get("offset") == "0" {
			io.WriteString(w, `{"data":"hello","next_offset":5,"eof":false}`)
		} else {
			io.WriteString(w, `{"data":"","next_offset":5,"eof":true}`)
		}
	}))
	defer srv.Close()
	for _, asJSON := range []bool{false, true} {
		cmd := newJobCommandsCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		args := []string{"--url", srv.URL, "x"}
		if asJSON {
			args = append(args, "--json")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "exited") {
			t.Fatal(out.String())
		}
	}
	for _, follow := range []bool{false, true} {
		cmd := newJobOutputCmd()
		var out bytes.Buffer
		cmd.SetOut(&out)
		args := []string{"--url", srv.URL, "x", "c"}
		if follow {
			args = append(args, "--follow")
		}
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.String() != "hello" {
			t.Fatal(out.String())
		}
	}
}

func TestExecValidation(t *testing.T) {
	for _, args := range [][]string{{"x", "-t", "-T"}, {"x", "--env", "BAD"}, {"x", "--timeout", "-1s"}, {"x", "--url", "https://user:pass@host"}} {
		cmd := newJobExecCmd()
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err == nil {
			t.Fatal(args)
		}
	}
}
