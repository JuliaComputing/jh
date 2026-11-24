package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const sampleJobInfo = `{
  "jobname": "jr-abc123",
  "jobname_alias": "train.jl",
  "status": "Failed",
  "product_name": "standard-batch",
  "product_display_name": "Julia batch job",
  "userid": "me@example.com",
  "cpu": 2,
  "memory": 8,
  "submittimestamp": "2026-11-24T10:00:00+00:00",
  "starttimestamp": "2026-11-24T10:01:00.250+00:00",
  "endtimestamp": "2026-11-24T10:04:30+00:00",
  "failure_reason": "Julia exited with status 1",
  "limit_type": "time",
  "limit_value": 2,
  "events": [{"message": "Job submitted", "timestamp": 1795514400}],
  "files": [{"name": "out.tar.gz", "type": "result", "size": 10, "hash": null, "upload_timestamp": "2026-11-24T10:04:31+00:00"}],
  "hibernation_enabled": false, "should_suspend": false, "suspended": false
}`

func TestFetchJobInfo(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		if strings.HasSuffix(r.URL.Path, "/jr-missing") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not found"}`))
			return
		}
		w.Write([]byte(sampleJobInfo))
	}))
	defer srv.Close()

	info, raw, err := fetchJobInfo(srv.Client(), srv.URL, "tok", "jr-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/jobs/jr-abc123" || gotAuth != "Bearer tok" {
		t.Errorf("request path=%q auth=%q", gotPath, gotAuth)
	}
	if info.Status != "Failed" || info.Alias != "train.jl" || info.FailureReason == "" || len(info.Events) != 1 || len(info.Files) != 1 {
		t.Errorf("unexpected info: %+v", info)
	}
	if !strings.Contains(string(raw), `"hibernation_enabled"`) {
		t.Error("raw document lost fields")
	}
	if err := jobOutcome(info); err == nil || !strings.Contains(err.Error(), "ended with status Failed: Julia exited") {
		t.Errorf("outcome = %v", err)
	}

	if _, _, err := fetchJobInfo(srv.Client(), srv.URL, "tok", "jr-missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing job: err = %v", err)
	}
}

func TestPrintJobInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(sampleJobInfo)) }))
	defer srv.Close()
	info, _, err := fetchJobInfo(srv.Client(), srv.URL, "tok", "jr-abc123")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	printJobInfo(&b, info, time.Now())
	out := b.String()
	for _, want := range []string{"jr-abc123 (train.jl)", "Failed", "2 vCPU, 8 GB", "Run time", "3m 30s", "Time limit", "2 h", "Julia exited with status 1", "jh job result list jr-abc123", "Job submitted"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestParseJobTime(t *testing.T) {
	for _, s := range []string{"2026-11-24T10:00:00+00:00", "2026-11-24T10:00:00.123+00:00", "2026-11-24T10:00:00.123456Z", "2026-11-24T10:00:00.123456"} {
		got, ok := parseJobTime(s)
		if !ok || got.UTC().Hour() != 10 {
			t.Errorf("parseJobTime(%q) = %v, %v", s, got, ok)
		}
	}
	if _, ok := parseJobTime("yesterday"); ok {
		t.Error("parsed garbage")
	}
}

func TestFormatJobDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second:                 "45s",
		3*time.Minute + 12*time.Second:   "3m 12s",
		2*time.Hour + 5*time.Minute + 59: "2h 5m",
	} {
		if got := formatJobDuration(d); got != want {
			t.Errorf("formatJobDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestFetchJobList(t *testing.T) {
	var gotQuery string
	body := `[{"jobname":"jr-b","jobname_alias":null,"status":"Running","userid":"other@example.com","is_owner":false,"submittimestamp":"2026-11-24T10:00:00.5+00:00","duration":65},
	          {"jobname":"jr-a","jobname_alias":"train.jl","status":"Completed","userid":"me@example.com","is_owner":true,"submittimestamp":"2026-11-23T10:00:00+00:00","duration":3600}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Query().Get("limit") == "5000" {
			w.Write([]byte(`{"success":false,"message":"Invalid parameter ` + "`limit`" + ` (must not exceed 2000). "}`))
			return
		}
		w.Write([]byte(body))
	}))
	defer srv.Close()

	entries, raw, err := fetchJobList(srv.Client(), srv.URL, "tok", 20)
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "limit=20" || len(entries) != 2 || len(raw) != 2 || entries[1].Alias == nil || *entries[1].Alias != "train.jl" {
		t.Errorf("query=%q entries=%+v", gotQuery, entries)
	}
	var b strings.Builder
	printJobList(&b, entries)
	out := b.String()
	for _, want := range []string{"USER", "other@example.com", "1m 5s", "1h 0m", "train.jl"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}

	// Without other users' jobs there is no USER column.
	b.Reset()
	printJobList(&b, entries[1:])
	if strings.Contains(b.String(), "USER") {
		t.Errorf("USER column for own jobs only:\n%s", b.String())
	}

	if _, _, err := fetchJobList(srv.Client(), srv.URL, "tok", 5000); err == nil || !strings.Contains(err.Error(), "must not exceed 2000") {
		t.Errorf("legacy failure: err = %v", err)
	}
}

func TestKillJob(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		if strings.HasSuffix(r.URL.Path, "/jr-done") {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"message":"Killing of job jr-done failed"}`))
			return
		}
		w.Write([]byte(`{"message":"Job jr-abc stopped successfully"}`))
	}))
	defer srv.Close()
	msg, err := killJob(srv.Client(), srv.URL, "tok", "jr-abc")
	if err != nil || msg != "Job jr-abc stopped successfully" || gotMethod != http.MethodDelete || gotPath != "/api/v1/jobs/jr-abc" {
		t.Errorf("kill: msg=%q err=%v %s %s", msg, err, gotMethod, gotPath)
	}
	if _, err := killJob(srv.Client(), srv.URL, "tok", "jr-done"); err == nil || !strings.Contains(err.Error(), "Killing of job jr-done failed") {
		t.Errorf("failed kill: err = %v", err)
	}
}

func TestWaitForJob(t *testing.T) {
	steps := []struct {
		status string
		err    error
	}{
		{"Submitted", nil},
		{"", &jobAPIError{Status: 502, Message: "bad gateway"}},
		{"Running", nil},
		{"Running", nil},
		{"Completed", nil},
	}
	i := 0
	get := func() (*JobInfo, error) {
		s := steps[i]
		i++
		if s.err != nil {
			return nil, s.err
		}
		return &JobInfo{JobName: "jr-x", Status: s.status}, nil
	}
	var seen []string
	clock := &fakeClock{now: time.Unix(0, 0)}
	info, err := waitForJob(get, 5*time.Second, clock, func(i *JobInfo) { seen = append(seen, i.Status) })
	if err != nil || info.Status != "Completed" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if strings.Join(seen, ",") != "Submitted,Running,Completed" {
		t.Errorf("status changes = %v", seen)
	}
	if len(clock.slept) != 4 {
		t.Errorf("slept %d times, want 4", len(clock.slept))
	}

	// A 4xx ends the wait at once.
	_, err = waitForJob(func() (*JobInfo, error) { return nil, &jobAPIError{Status: 403, Message: "no"} }, time.Second, &fakeClock{}, nil)
	var apiErr *jobAPIError
	if !errors.As(err, &apiErr) {
		t.Errorf("403: err = %v", err)
	}

	// Persistent 5xx gives up after maxStatusPollFailures.
	calls := 0
	_, err = waitForJob(func() (*JobInfo, error) { calls++; return nil, &jobAPIError{Status: 500, Message: "boom"} }, time.Second, &fakeClock{}, nil)
	if err == nil || calls != maxStatusPollFailures {
		t.Errorf("5xx: calls=%d err=%v", calls, err)
	}
}

func TestDecodeJobLogPage(t *testing.T) {
	array := `[{"_meta":true,"end":"top"},{"eventId":"0","timestamp":1,"message":"hello\n","metadata":{"level":"Info"}},{"eventId":1,"timestamp":2,"message":"bye","metadata":{"level":"Warn"}},{"_meta":true,"end":"bottom"}]`
	for desc, body := range map[string]string{
		"raw":     array,
		"wrapped": `"` + strings.ReplaceAll(strings.ReplaceAll(array, `\`, `\\`), `"`, `\"`) + `"`,
	} {
		page, err := decodeJobLogPage([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", desc, err)
		}
		if len(page.Entries) != 2 || !page.AtEnd || page.Entries[1].EventID != "1" || page.Entries[0].Message != "hello\n" {
			t.Errorf("%s: page = %+v", desc, page)
		}
	}
	for _, empty := range []string{``, `""`, `[]`} {
		page, err := decodeJobLogPage([]byte(empty))
		if err != nil || len(page.Entries) != 0 || page.AtEnd {
			t.Errorf("empty %q: page=%+v err=%v", empty, page, err)
		}
	}
}

func TestFetchJobLogsRequest(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.Query()
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	if _, err := fetchJobLogs(srv.Client(), srv.URL, "tok", "jr-x", jobLogQuery{EventID: "41", N: 1000}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/logs/jobs/jr-x" || gotQuery.Get("direction") != "forward" || gotQuery.Get("event_id") != "41" ||
		gotQuery.Get("log_out_type") != "json" || gotQuery.Get("response_format") != "json" || gotQuery.Get("nentries") != "1000" {
		t.Errorf("request %s %v", gotPath, gotQuery)
	}
}

// fakeLog serves a growing log in pages, as the log API does.
type fakeLog struct {
	entries []jobLogEntry
	ended   bool // the job finished and every entry is available
	queries []jobLogQuery
}

func (f *fakeLog) fetch(q jobLogQuery) (*jobLogPage, error) {
	f.queries = append(f.queries, q)
	var page []jobLogEntry
	if q.Backward {
		page = f.entries[max(0, len(f.entries)-q.N):]
		return &jobLogPage{Entries: page, AtEnd: f.ended}, nil
	}
	start := 0
	for i, e := range f.entries {
		if string(e.EventID) == q.EventID {
			start = i + 1
		}
	}
	end := min(len(f.entries), start+q.N)
	page = f.entries[start:end]
	return &jobLogPage{Entries: page, AtEnd: f.ended && end == len(f.entries)}, nil
}

func logEntries(msgs ...string) []jobLogEntry {
	out := make([]jobLogEntry, len(msgs))
	for i, m := range msgs {
		out[i] = jobLogEntry{EventID: flexString(string(rune('0' + i))), Message: m}
	}
	return out
}

func TestJobLogStreamerCatchUpAndTail(t *testing.T) {
	log := &fakeLog{entries: logEntries("a", "b", "c")}
	var b strings.Builder
	s := &jobLogStreamer{fetch: log.fetch, out: &b}
	if n, err := s.catchUp(); err != nil || n != 3 || b.String() != "a\nb\nc\n" {
		t.Errorf("catchUp: n=%d err=%v out=%q", n, err, b.String())
	}
	log.entries = append(log.entries, logEntries("a", "b", "c", "d")[3])
	b.Reset()
	if n, _ := s.catchUp(); n != 1 || b.String() != "d\n" {
		t.Errorf("second catchUp printed %q", b.String())
	}

	b.Reset()
	s = &jobLogStreamer{fetch: log.fetch, out: &b}
	if err := s.tail(2); err != nil || b.String() != "c\nd\n" || s.lastEvent != "3" {
		t.Errorf("tail: out=%q last=%q err=%v", b.String(), s.lastEvent, err)
	}
}

func TestJobLogStreamerFollow(t *testing.T) {
	// The log grows while the job runs, then ends with the bottom marker.
	log := &fakeLog{}
	polls := 0
	status := func() (string, error) {
		polls++
		switch polls {
		case 1:
			return "Submitted", nil
		case 2:
			log.entries = logEntries("start")
			return "Running", nil
		default:
			log.entries = logEntries("start", "done")
			log.ended = true
			return "Completed", nil
		}
	}
	var b strings.Builder
	s := &jobLogStreamer{fetch: log.fetch, out: &b}
	if err := s.follow(status, time.Second, &fakeClock{}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "start\ndone\n" {
		t.Errorf("followed %q", b.String())
	}

	// A finished job whose log never shows the end marker: stop after the
	// drain polls.
	log = &fakeLog{entries: logEntries("x")}
	b.Reset()
	s = &jobLogStreamer{fetch: log.fetch, out: &b}
	clock := &fakeClock{}
	if err := s.follow(func() (string, error) { return "Failed", nil }, time.Second, clock); err != nil {
		t.Fatal(err)
	}
	if b.String() != "x\n" || len(clock.slept) != jobLogDrainPolls {
		t.Errorf("drain: out=%q slept=%d", b.String(), len(clock.slept))
	}

	// The runtime's last line ends the log as soon as the job has finished.
	log = &fakeLog{entries: logEntries("x", "Julia exited with status = 0")}
	b.Reset()
	s = &jobLogStreamer{fetch: log.fetch, out: &b}
	clock = &fakeClock{}
	if err := s.follow(func() (string, error) { return "Completed", nil }, time.Second, clock); err != nil {
		t.Fatal(err)
	}
	if !s.sawExit || len(clock.slept) != 1 || clock.slept[0] != time.Second {
		t.Errorf("exit line: sawExit=%v slept=%v", s.sawExit, clock.slept)
	}

	// A job that failed to submit has no log: stop at once.
	clock = &fakeClock{}
	s = &jobLogStreamer{fetch: (&fakeLog{}).fetch, out: &b}
	if err := s.follow(func() (string, error) { return "SubmitFailed", nil }, time.Second, clock); err != nil || len(clock.slept) != 0 {
		t.Errorf("SubmitFailed: err=%v slept=%d", err, len(clock.slept))
	}
}

func TestFormatJobLogEntry(t *testing.T) {
	e := jobLogEntry{Message: "hi\n", Timestamp: 0}
	e.Metadata.Level = "Warn"
	if got := formatJobLogEntry(e, false); got != "hi" {
		t.Errorf("plain = %q", got)
	}
	if got := formatJobLogEntry(e, true); !strings.Contains(got, "Warn  hi") {
		t.Errorf("with timestamps = %q", got)
	}

	// The log service sends an empty line as the JSON-encoded record.
	e.Message = `{"metadata":{"group":"system","level":"Info"},"group":"g","logstream":"s","keywords":{"jrun_hostname":"jr-x"}}`
	if got := formatJobLogEntry(e, false); got != "" {
		t.Errorf("raw record = %q", got)
	}
	for _, msg := range []string{`{"logstream":"s","keywords":{},"message":"m"}`, `{"a":1}`, `{"logstream": not json`} {
		e.Message = msg
		if got := formatJobLogEntry(e, false); got != msg {
			t.Errorf("%s printed as %q", msg, got)
		}
	}
}
