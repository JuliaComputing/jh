package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// JobEvent is one entry of a job's event history; Timestamp is in Unix seconds.
type JobEvent struct {
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

// JobInfo is the subset of GET /api/v1/jobs/{job_name} the CLI shows.
type JobInfo struct {
	JobName            string     `json:"jobname"`
	Alias              string     `json:"jobname_alias"`
	Status             string     `json:"status"`
	ProductName        string     `json:"product_name"`
	ProductDisplayName string     `json:"product_display_name"`
	UserID             string     `json:"userid"`
	CPU                *int       `json:"cpu"`
	Memory             *float64   `json:"memory"`
	GPUCount           *int       `json:"gpu_count"`
	GPUType            *string    `json:"gpu_type"`
	Workers            *int       `json:"workers"`
	ProcessesPerNode   *int       `json:"processes_per_node"`
	ProxyLink          string     `json:"proxy_link"`
	SubmitTimestamp    *string    `json:"submittimestamp"`
	StartTimestamp     *string    `json:"starttimestamp"`
	EndTimestamp       *string    `json:"endtimestamp"`
	FailureReason      string     `json:"failure_reason"`
	ActCost            *float64   `json:"act_cost"`
	LimitType          string     `json:"limit_type"`
	LimitValue         *float64   `json:"limit_value"`
	Events             []JobEvent `json:"events"`
	Files              []JobFile  `json:"files"`
}

// isTerminalJobStatus reports whether a job in this status will not change
// any more.
func isTerminalJobStatus(status string) bool {
	switch status {
	case "SubmitFailed", "Failed", "Stopped", "Completed":
		return true
	}
	return false
}

// fetchJobInfo fetches a job's details. raw is the server's document as it was
// sent, for --json.
func fetchJobInfo(client *http.Client, baseURL, idToken, jobName string) (info *JobInfo, raw json.RawMessage, err error) {
	body, err := jobAPIDo(client, http.MethodGet, baseURL+"/api/v1/jobs/"+url.PathEscape(jobName), idToken, "", nil)
	if err != nil {
		var apiErr *jobAPIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, nil, fmt.Errorf("job '%s' not found", jobName)
		}
		return nil, nil, err
	}
	info = &JobInfo{}
	if err := json.Unmarshal(body, info); err != nil {
		return nil, nil, fmt.Errorf("failed to parse response: %w", err)
	}
	if info.JobName == "" || info.Status == "" {
		return nil, nil, fmt.Errorf("unexpected response: %s", strings.TrimSpace(string(body)))
	}
	return info, body, nil
}

// killJob stops a job and returns the server's message.
func killJob(client *http.Client, baseURL, idToken, jobName string) (string, error) {
	body, err := jobAPIDo(client, http.MethodDelete, baseURL+"/api/v1/jobs/"+url.PathEscape(jobName), idToken, "", nil)
	if err != nil {
		return "", err
	}
	var parsed struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &parsed)
	if parsed.Message == "" {
		parsed.Message = "Job " + jobName + " stopped"
	}
	return parsed.Message, nil
}

// parseJobTime parses the timestamps job APIs send: ISO 8601 with an offset,
// or without one (UTC).
func parseJobTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation("2006-01-02T15:04:05.999999999", s, time.UTC); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func formatJobTime(s *string) string {
	if s == nil {
		return ""
	}
	t, ok := parseJobTime(*s)
	if !ok {
		return *s
	}
	return t.Local().Format("2006-01-02 15:04:05")
}

// formatJobDuration renders a duration as e.g. "45s", "3m 12s" or "2h 5m".
func formatJobDuration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// jobRunTime is how long a job has run: start to end, or start to now while
// it is still running. ok is false before it starts.
func jobRunTime(info *JobInfo, now time.Time) (time.Duration, bool) {
	if info.StartTimestamp == nil {
		return 0, false
	}
	start, ok := parseJobTime(*info.StartTimestamp)
	if !ok {
		return 0, false
	}
	end := now
	if info.EndTimestamp != nil {
		if t, ok := parseJobTime(*info.EndTimestamp); ok {
			end = t
		}
	}
	if end.Before(start) {
		return 0, true
	}
	return end.Sub(start), true
}

func jobInfoDisplayName(info *JobInfo) string {
	if info.Alias != "" {
		return fmt.Sprintf("%s (%s)", info.JobName, info.Alias)
	}
	return info.JobName
}

func writeField(w io.Writer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(w, "  %s%s\n", dim(pad(label, fieldLabelWidth)), value)
}

func printJobInfo(w io.Writer, info *JobInfo, now time.Time) {
	fmt.Fprintf(w, "%s  %s\n", bold(jobInfoDisplayName(info)), info.Status)
	product := info.ProductDisplayName
	if product == "" {
		product = info.ProductName
	}
	writeField(w, "Product", product)
	if info.CPU != nil || info.Memory != nil {
		var parts []string
		if info.CPU != nil {
			parts = append(parts, fmt.Sprintf("%d vCPU", *info.CPU))
		}
		if info.Memory != nil {
			parts = append(parts, strconv.FormatFloat(*info.Memory, 'f', -1, 64)+" GB")
		}
		if info.GPUCount != nil && *info.GPUCount > 0 {
			gpu := fmt.Sprintf("%d× GPU", *info.GPUCount)
			if info.GPUType != nil && *info.GPUType != "" {
				gpu = fmt.Sprintf("%d× %s", *info.GPUCount, *info.GPUType)
			}
			parts = append(parts, gpu)
		}
		writeField(w, "Resources", strings.Join(parts, ", "))
	}
	if info.Workers != nil && *info.Workers > 0 {
		writeField(w, "Workers", strconv.Itoa(*info.Workers))
	}
	writeField(w, "Submitted", formatJobTime(info.SubmitTimestamp))
	writeField(w, "Started", formatJobTime(info.StartTimestamp))
	writeField(w, "Ended", formatJobTime(info.EndTimestamp))
	if d, ok := jobRunTime(info, now); ok {
		writeField(w, "Run time", formatJobDuration(d))
	}
	switch {
	case info.LimitType == "unlimited":
		writeField(w, "Time limit", "unlimited")
	case info.LimitValue != nil:
		writeField(w, "Time limit", strconv.FormatFloat(*info.LimitValue, 'f', -1, 64)+" h")
	}
	if info.ActCost != nil {
		writeField(w, "Cost", fmt.Sprintf("%.2f", *info.ActCost))
	}
	writeField(w, "Failure", info.FailureReason)
	writeField(w, "Link", info.ProxyLink)
	if n := len(filterJobFiles(info.Files, jobResultFileType)); n > 0 {
		writeField(w, "Results", fmt.Sprintf("%s (jh job result list %s)", formatCount(n), info.JobName))
	}
	if len(info.Events) > 0 {
		fmt.Fprintln(w, bold("Events"))
		for _, e := range info.Events {
			ts := time.Unix(e.Timestamp, 0).Local().Format("2006-01-02 15:04:05")
			fmt.Fprintf(w, "  %s  %s\n", dim(ts), e.Message)
		}
	}
}

// jobListEntry is the subset of a GET /juliaruncloud/get_jobs row the CLI
// shows. The listing covers every user's jobs for administrators, with
// IsOwner false on other users' jobs.
type jobListEntry struct {
	JobName         string  `json:"jobname"`
	Alias           *string `json:"jobname_alias"`
	Status          string  `json:"status"`
	UserID          string  `json:"userid"`
	IsOwner         *bool   `json:"is_owner"`
	ProductName     *string `json:"product_name"`
	SubmitTimestamp *string `json:"submittimestamp"`
	Duration        *int64  `json:"duration"`
}

// maxJobListLimit is the server's cap on get_jobs' limit.
const maxJobListLimit = 2000

// fetchJobList lists the most recent jobs, newest first. raw holds each row
// as the server sent it, for --json.
func fetchJobList(client *http.Client, baseURL, idToken string, limit int) ([]jobListEntry, []json.RawMessage, error) {
	u := baseURL + "/juliaruncloud/get_jobs?limit=" + strconv.Itoa(limit)
	body, err := jobAPIDo(client, http.MethodGet, u, idToken, "", nil)
	if err != nil {
		return nil, nil, err
	}
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("{")) {
		// The legacy failure envelope, sent with status 200.
		var failed struct {
			Success *bool  `json:"success"`
			Message string `json:"message"`
		}
		if json.Unmarshal(trimmed, &failed) == nil && failed.Message != "" {
			return nil, nil, errors.New(strings.TrimSpace(failed.Message))
		}
		return nil, nil, fmt.Errorf("unexpected response: %s", trimmed)
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, nil, fmt.Errorf("failed to parse response: %w", err)
	}
	entries := make([]jobListEntry, len(raw))
	for i, r := range raw {
		if err := json.Unmarshal(r, &entries[i]); err != nil {
			return nil, nil, fmt.Errorf("failed to parse response: %w", err)
		}
	}
	return entries, raw, nil
}

func printJobList(w io.Writer, entries []jobListEntry) {
	if len(entries) == 0 {
		fmt.Fprintln(w, "No jobs.")
		return
	}
	showUser := false
	nameWidth := len("NAME")
	jobWidth := len("JOB")
	for _, e := range entries {
		if e.IsOwner != nil && !*e.IsOwner {
			showUser = true
		}
		if e.Alias != nil {
			nameWidth = max(nameWidth, utf8.RuneCountInString(*e.Alias))
		}
		jobWidth = max(jobWidth, utf8.RuneCountInString(e.JobName))
	}
	nameWidth = min(nameWidth, 30)

	header := pad("JOB", jobWidth) + "  " + pad("NAME", nameWidth) + "  " + pad("STATUS", 12) + "  " + pad("SUBMITTED", 19) + "  " + padLeft("RUN TIME", 9)
	if showUser {
		header += "  USER"
	}
	fmt.Fprintln(w, bold(header))
	for _, e := range entries {
		alias := ""
		if e.Alias != nil {
			alias = *e.Alias
		}
		runTime := ""
		if e.Duration != nil && *e.Duration > 0 {
			runTime = formatJobDuration(time.Duration(*e.Duration) * time.Second)
		}
		line := pad(e.JobName, jobWidth) + "  " + pad(truncate(alias, nameWidth), nameWidth) + "  " + pad(e.Status, 12) + "  " + pad(formatJobTime(e.SubmitTimestamp), 19) + "  " + padLeft(runTime, 9)
		if showUser {
			line += "  " + e.UserID
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

// waitForJob polls a job's status every interval until it reaches a terminal
// status. onChange is called with the first status and every change after it.
// Network errors and 5xx answers are retried up to maxStatusPollFailures in a
// row; other errors end the wait.
func waitForJob(get func() (*JobInfo, error), interval time.Duration, clock pollClock, onChange func(*JobInfo)) (*JobInfo, error) {
	failures := 0
	last := ""
	for {
		info, err := get()
		if err != nil {
			var apiErr *jobAPIError
			if errors.As(err, &apiErr) && apiErr.Status < 500 {
				return nil, err
			}
			if strings.Contains(err.Error(), "not found") {
				return nil, err
			}
			failures++
			if failures >= maxStatusPollFailures {
				return nil, fmt.Errorf("checking the job status failed %d times in a row: %w", failures, err)
			}
		} else {
			failures = 0
			if info.Status != last && onChange != nil {
				onChange(info)
			}
			last = info.Status
			if isTerminalJobStatus(info.Status) {
				return info, nil
			}
		}
		clock.Sleep(interval)
	}
}

// flexString decodes a JSON string or number as a string.
type flexString string

func (s *flexString) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = flexString(str)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = flexString(n.String())
	return nil
}

// jobLogEntry is one element of the log API's JSON array: a log entry, or a
// marker ({"_meta": true, "end": "top"|"bottom"}) for the start of the log
// or, once the job has finished, its end.
type jobLogEntry struct {
	EventID   flexString `json:"eventId"`
	Timestamp int64      `json:"timestamp"`
	Message   string     `json:"message"`
	Metadata  struct {
		Level string `json:"level"`
	} `json:"metadata"`
	Meta bool   `json:"_meta"`
	End  string `json:"end"`
}

// jobLogPage is one page of log entries, oldest first. AtEnd reports the
// bottom marker: the job has finished and this page reaches its last entry.
type jobLogPage struct {
	Entries []jobLogEntry
	AtEnd   bool
}

// jobLogQuery selects a page of up to N entries: a backward page holds the
// newest entries, a forward page starts at the oldest entry, or strictly
// after EventID when it is set.
type jobLogQuery struct {
	Backward bool
	EventID  string
	N        int
}

// decodeJobLogPage decodes a log page. Servers answer response_format=json
// with the array itself; ones that ignore the parameter send the array
// JSON-encoded as a string.
func decodeJobLogPage(body []byte) (*jobLogPage, error) {
	body = bytes.TrimSpace(body)
	if bytes.HasPrefix(body, []byte(`"`)) {
		var inner string
		if err := json.Unmarshal(body, &inner); err != nil {
			return nil, fmt.Errorf("failed to parse logs: %w", err)
		}
		body = bytes.TrimSpace([]byte(inner))
	}
	page := &jobLogPage{}
	if len(body) == 0 {
		return page, nil
	}
	var items []jobLogEntry
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("failed to parse logs: %w", err)
	}
	for _, it := range items {
		if it.Meta {
			if it.End == "bottom" {
				page.AtEnd = true
			}
			continue
		}
		page.Entries = append(page.Entries, it)
	}
	return page, nil
}

func fetchJobLogs(client *http.Client, baseURL, idToken, jobName string, q jobLogQuery) (*jobLogPage, error) {
	params := url.Values{}
	params.Set("log_out_type", "json")
	params.Set("response_format", "json")
	if q.N > 0 {
		params.Set("nentries", strconv.Itoa(q.N))
	}
	if q.Backward {
		params.Set("direction", "backward")
	} else {
		params.Set("direction", "forward")
	}
	if q.EventID != "" {
		params.Set("event_id", q.EventID)
	}
	u := baseURL + "/api/v1/logs/jobs/" + url.PathEscape(jobName) + "?" + params.Encode()
	body, err := jobAPIDo(client, http.MethodGet, u, idToken, "", nil)
	if err != nil {
		var apiErr *jobAPIError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, fmt.Errorf("no logs for job '%s': %s", jobName, apiErr.Message)
		}
		return nil, err
	}
	return decodeJobLogPage(body)
}

// jobLogPageSize is the number of entries requested per page.
const jobLogPageSize = 1000

func formatJobLogEntry(e jobLogEntry, timestamps bool) string {
	msg := strings.TrimRight(e.Message, "\n")
	if isRawLogRecord(msg) {
		msg = ""
	}
	if !timestamps {
		return msg
	}
	ts := time.UnixMilli(e.Timestamp).Local().Format("2006-01-02 15:04:05.000")
	level := e.Metadata.Level
	if level == "" {
		level = "-"
	}
	return dim(ts) + " " + pad(level, 5) + " " + msg
}

// isRawLogRecord reports a message that is the log record itself, JSON-encoded
// without a "message" field: the log service's rendering of an empty line.
func isRawLogRecord(msg string) bool {
	if !strings.HasPrefix(msg, "{") || !strings.Contains(msg, `"logstream"`) {
		return false
	}
	var rec map[string]json.RawMessage
	if json.Unmarshal([]byte(msg), &rec) != nil {
		return false
	}
	_, hasStream := rec["logstream"]
	_, hasKeywords := rec["keywords"]
	_, hasMessage := rec["message"]
	return hasStream && hasKeywords && !hasMessage
}

// jobLogStreamer prints a job's log, page by page, remembering where it got
// to so it can be called again to print what was added since.
type jobLogStreamer struct {
	fetch      func(jobLogQuery) (*jobLogPage, error)
	out        io.Writer
	timestamps bool
	lastEvent  string
	atEnd      bool
	sawExit    bool // printed the runtime's last line
}

func (s *jobLogStreamer) print(entries []jobLogEntry) {
	for _, e := range entries {
		fmt.Fprintln(s.out, formatJobLogEntry(e, s.timestamps))
		s.lastEvent = string(e.EventID)
		if strings.HasPrefix(e.Message, jobLogExitPrefix) {
			s.sawExit = true
		}
	}
}

// tail prints the last n entries.
func (s *jobLogStreamer) tail(n int) error {
	page, err := s.fetch(jobLogQuery{Backward: true, N: n})
	if err != nil {
		return err
	}
	s.print(page.Entries)
	s.atEnd = page.AtEnd
	return nil
}

// catchUp prints every entry after the last one printed (from the start when
// none was), and returns how many it printed.
func (s *jobLogStreamer) catchUp() (int, error) {
	printed := 0
	for {
		page, err := s.fetch(jobLogQuery{EventID: s.lastEvent, N: jobLogPageSize})
		if err != nil {
			return printed, err
		}
		s.print(page.Entries)
		printed += len(page.Entries)
		s.atEnd = page.AtEnd
		if page.AtEnd || len(page.Entries) < jobLogPageSize {
			return printed, nil
		}
	}
}

// jobLogDrainPolls is how many polls after a job finishes may come back empty
// before following its log stops without having seen its end. Log delivery
// lags the job by seconds, and the server adds the end marker minutes after
// the job finishes, so the runtime's last line (jobLogExitPrefix) is the
// usual end; a job stopped or failed before Julia exited never logs it, and
// one that never ran has no log at all.
const jobLogDrainPolls = 6

// jobLogExitPrefix starts the line the job runtime logs when Julia exits; the
// runner may add a line or two after it.
const jobLogExitPrefix = "Julia exited with status"

// follow prints new entries every interval until the job has finished and
// its log has been read to the end. status reports the job's current status.
func (s *jobLogStreamer) follow(status func() (string, error), interval time.Duration, clock pollClock) error {
	emptyAfterEnd := 0
	failures := 0
	for {
		printed, err := s.catchUp()
		if err != nil {
			var apiErr *jobAPIError
			if errors.As(err, &apiErr) && apiErr.Status < 500 {
				return err
			}
			failures++
			if failures >= maxStatusPollFailures {
				return fmt.Errorf("reading the job log failed %d times in a row: %w", failures, err)
			}
			clock.Sleep(interval)
			continue
		}
		failures = 0
		if s.atEnd {
			return nil
		}
		st, err := status()
		if err == nil && st == "SubmitFailed" {
			return nil // never ran, so it has no log
		}
		if err == nil && isTerminalJobStatus(st) {
			if printed == 0 {
				emptyAfterEnd++
			} else {
				emptyAfterEnd = 0
			}
			if s.sawExit {
				// The runner may log a line or two after Julia's exit.
				clock.Sleep(min(interval, time.Second))
				_, err := s.catchUp()
				return err
			}
			if emptyAfterEnd >= jobLogDrainPolls {
				return nil
			}
		}
		clock.Sleep(interval)
	}
}

// jobPollInterval is how often waiting polls a job's status, and following
// polls its log.
const jobPollInterval = 5 * time.Second

// jobClient is a job API client whose token is refreshed before every
// request, so waiting on a job that runs longer than a token lives works.
type jobClient struct {
	client  *http.Client
	baseURL string
	token   func() (string, error)
}

func newJobClient(server string) *jobClient {
	return &jobClient{
		client:  &http.Client{Timeout: 60 * time.Second},
		baseURL: jobBaseURL(server),
		token: func() (string, error) {
			t, err := ensureValidToken()
			if err != nil {
				return "", fmt.Errorf("authentication required: %w", err)
			}
			return t.IDToken, nil
		},
	}
}

func (c *jobClient) info(jobName string) (*JobInfo, json.RawMessage, error) {
	tok, err := c.token()
	if err != nil {
		return nil, nil, err
	}
	return fetchJobInfo(c.client, c.baseURL, tok, jobName)
}

func (c *jobClient) logs(jobName string, q jobLogQuery) (*jobLogPage, error) {
	tok, err := c.token()
	if err != nil {
		return nil, err
	}
	return fetchJobLogs(c.client, c.baseURL, tok, jobName, q)
}

// detachOnInterrupt makes Ctrl+C while waiting on a job say that the job
// keeps running before exiting. The returned function restores the default.
func detachOnInterrupt(jobName string) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			fmt.Fprintf(os.Stderr, "\nStopped waiting. Job %s keeps running on JuliaHub: 'jh job status %s' checks on it, 'jh job kill %s' stops it.\n", jobName, jobName, jobName)
			os.Exit(130)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

// watchJob waits for a job to finish, following its log on stdout when
// follow is set and reporting status changes on stderr. It then prints the
// job's details (as JSON with asJSON) unless following, and returns an
// *errJobNotCompleted when the job did not complete.
func watchJob(server, jobName string, follow, asJSON, timestamps bool) error {
	c := newJobClient(server)
	defer detachOnInterrupt(jobName)()

	onChange := func(info *JobInfo) {
		fmt.Fprintf(os.Stderr, "%s %s %s\n", dim(time.Now().Format("15:04:05")), jobName, info.Status)
	}

	last := ""
	if follow {
		status := func() (string, error) {
			info, _, err := c.info(jobName)
			if err != nil {
				return "", err
			}
			if info.Status != last {
				onChange(info)
				last = info.Status
			}
			return info.Status, nil
		}
		s := &jobLogStreamer{
			fetch:      func(q jobLogQuery) (*jobLogPage, error) { return c.logs(jobName, q) },
			out:        os.Stdout,
			timestamps: timestamps,
		}
		if err := s.follow(status, jobPollInterval, realClock{}); err != nil {
			return err
		}
	}

	var raw json.RawMessage
	get := func() (*JobInfo, error) {
		info, r, err := c.info(jobName)
		raw = r
		return info, err
	}
	var onWaitChange func(*JobInfo)
	if !follow {
		onWaitChange = onChange
	}
	info, err := waitForJob(get, jobPollInterval, realClock{}, onWaitChange)
	if err != nil {
		return err
	}
	switch {
	case asJSON:
		if err := writeIndentedRaw(os.Stdout, raw); err != nil {
			return err
		}
	case follow && info.Status != last:
		fmt.Fprintf(os.Stderr, "%s %s %s\n", dim(time.Now().Format("15:04:05")), jobName, info.Status)
	default:
		printJobInfo(os.Stdout, info, time.Now())
	}
	outcome := jobOutcome(info)
	if outcome != nil && !follow && info.Status != "SubmitFailed" {
		fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf("Its log: jh job logs %s", jobName)))
	}
	return outcome
}

// writeIndentedRaw writes a JSON document 2-space indented with a trailing
// newline, keeping every field the server sent.
func writeIndentedRaw(w io.Writer, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// showJobStatus is `jh job status`.
func showJobStatus(server, jobName string, asJSON bool) error {
	info, raw, err := newJobClient(server).info(jobName)
	if err != nil {
		return err
	}
	if asJSON {
		return writeIndentedRaw(os.Stdout, raw)
	}
	printJobInfo(os.Stdout, info, time.Now())
	return nil
}

// listJobs is `jh job list`.
func listJobs(server string, limit int, asJSON bool) error {
	if limit < 1 || limit > maxJobListLimit {
		return fmt.Errorf("--limit must be between 1 and %d", maxJobListLimit)
	}
	c := newJobClient(server)
	tok, err := c.token()
	if err != nil {
		return err
	}
	entries, raw, err := fetchJobList(c.client, c.baseURL, tok, limit)
	if err != nil {
		return err
	}
	if asJSON {
		if raw == nil {
			raw = []json.RawMessage{}
		}
		return writeJSON(os.Stdout, map[string][]json.RawMessage{"jobs": raw})
	}
	printJobList(os.Stdout, entries)
	return nil
}

// runJobKill is `jh job kill`.
func runJobKill(server, jobName string) error {
	c := newJobClient(server)
	tok, err := c.token()
	if err != nil {
		return err
	}
	msg, err := killJob(c.client, c.baseURL, tok, jobName)
	if err != nil {
		return err
	}
	fmt.Println(msg)
	return nil
}

// showJobLogs is `jh job logs`: the whole log, or its last tail entries,
// then with follow every new entry until the job has finished.
func showJobLogs(server, jobName string, follow bool, tail int, timestamps bool) error {
	if tail < 0 {
		return errors.New("--tail must not be negative")
	}
	c := newJobClient(server)
	s := &jobLogStreamer{
		fetch:      func(q jobLogQuery) (*jobLogPage, error) { return c.logs(jobName, q) },
		out:        os.Stdout,
		timestamps: timestamps,
	}
	if tail > 0 {
		if err := s.tail(tail); err != nil {
			return err
		}
	} else if !follow {
		_, err := s.catchUp()
		return err
	}
	if !follow {
		return nil
	}
	defer detachOnInterrupt(jobName)()
	status := func() (string, error) {
		info, _, err := c.info(jobName)
		if err != nil {
			return "", err
		}
		return info.Status, nil
	}
	return s.follow(status, jobPollInterval, realClock{})
}
