package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const sampleJobUsage = `{"jobs":{"used":3,"limit":50},"vcpus":{"used":96,"limit":1000},"gpus":{"used":0,"limit":10}}`

func TestFetchJobUsage(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Write([]byte(sampleJobUsage))
	}))
	defer srv.Close()

	usage, err := fetchJobUsage(srv.Client(), srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/jobs/usage" || gotAuth != "Bearer tok" {
		t.Errorf("request path=%q auth=%q", gotPath, gotAuth)
	}
	want := JobUsage{
		Jobs:  ResourceUsage{Used: 3, Limit: 50},
		VCPUs: ResourceUsage{Used: 96, Limit: 1000},
		GPUs:  ResourceUsage{Used: 0, Limit: 10},
	}
	if *usage != want {
		t.Errorf("usage = %+v, want %+v", *usage, want)
	}
}

func TestFetchJobUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		check  func(error) bool
		desc   string
	}{
		// Servers without the endpoint route the request to
		// GET /api/v1/jobs/{job_name} as a job called "usage", which 404s.
		{http.StatusNotFound, `{"message":"Not found"}`, func(err error) bool { return errors.Is(err, errJobUsageUnsupported) }, "unsupported"},
		{http.StatusForbidden, `{"message":"Not allowed"}`, func(err error) bool { return strings.Contains(err.Error(), "not allowed to run jobs") }, "forbidden"},
		{http.StatusInternalServerError, "boom", func(err error) bool { return strings.Contains(err.Error(), "status 500") }, "server error"},
		{http.StatusOK, `{"jobs":{"used":1,"limit":2}}`, func(err error) bool { return strings.Contains(err.Error(), "missing") }, "incomplete response"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := fetchJobUsage(srv.Client(), srv.URL, "tok")
			if err == nil || !tc.check(err) {
				t.Errorf("got error %v", err)
			}
		})
	}
}

func TestPrintJobUsage(t *testing.T) {
	usage := JobUsage{
		Jobs:  ResourceUsage{Used: 3, Limit: 50},
		VCPUs: ResourceUsage{Used: 960, Limit: 1000},
		GPUs:  ResourceUsage{Used: 0, Limit: 10},
	}
	var buf bytes.Buffer
	printJobUsage(&buf, "example.juliahub.com", usage)
	want := `Compute usage on example.juliahub.com
  Jobs          3 of 50  (6%)
  vCPUs         960 of 1,000  (96%)
  GPUs          0 of 10
`
	if got := buf.String(); got != want {
		t.Errorf("output:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintJobUsageAtLimit(t *testing.T) {
	usage := JobUsage{
		Jobs:  ResourceUsage{Used: 50, Limit: 50},
		VCPUs: ResourceUsage{Used: 0, Limit: 1000},
		GPUs:  ResourceUsage{Used: 0, Limit: 0},
	}
	var buf bytes.Buffer
	printJobUsage(&buf, "example.juliahub.com", usage)
	if got := buf.String(); !strings.Contains(got, "  Jobs          50 of 50  (100%, at limit)\n") ||
		!strings.Contains(got, "  GPUs          0 of 0\n") {
		t.Errorf("output:\n%s", got)
	}
}
