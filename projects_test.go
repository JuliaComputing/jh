package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestEscapeLikePattern(t *testing.T) {
	cases := map[string]string{
		"john":        "john",
		"a_b":         `a\_b`,
		"100%":        `100\%`,
		`back\slash`:  `back\\slash`,
		"mix_%\\done": `mix\_\%\\done`,
	}
	for in, want := range cases {
		if got := escapeLikePattern(in); got != want {
			t.Errorf("escapeLikePattern(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildProjectsFilter(t *testing.T) {
	t.Run("no filter fetches everything visible", func(t *testing.T) {
		got := buildProjectsFilter("", false, 42)
		if !reflect.DeepEqual(got, map[string]interface{}{}) {
			t.Errorf("got %v, want empty filter", got)
		}
	})

	t.Run("--user with no value filters by the current user's id", func(t *testing.T) {
		got := buildProjectsFilter("", true, 42)
		want := map[string]interface{}{
			"owner_id": map[string]interface{}{"_eq": int64(42)},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("--user <name> filters by owner username, case-insensitively and literally", func(t *testing.T) {
		got := buildProjectsFilter("Jo_hn", true, 42)
		want := map[string]interface{}{
			"owner": map[string]interface{}{
				"username": map[string]interface{}{"_ilike": `Jo\_hn`},
			},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestTotalPages(t *testing.T) {
	cases := map[int]int{
		0:                    0,
		1:                    1,
		projectsPageSize:     1,
		projectsPageSize + 1: 2,
		2 * projectsPageSize: 2,
		57508:                576,
	}
	for total, want := range cases {
		if got := totalPages(total); got != want {
			t.Errorf("totalPages(%d) = %d, want %d", total, got, want)
		}
	}
}

func TestBuildAddProjectRequest(t *testing.T) {
	t.Run("defaults: private, collaborative, not deployable", func(t *testing.T) {
		got, err := buildAddProjectRequest(projectCreateOptions{
			Name:        "  my-analysis ",
			Description: "Q3 study",
			Tags:        []string{"finance", " ", " q3 "},
		})
		if err != nil {
			t.Fatal(err)
		}
		want := AddProjectRequest{
			Name:                "my-analysis",
			Description:         "Q3 study",
			EditingMode:         "collaborative",
			InstanceDefaultRole: "No Access",
			RequireApproval:     true,
			Tags:                []string{"finance", "q3"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("JSON field names match the server's AddProjectRequest", func(t *testing.T) {
		req, err := buildAddProjectRequest(projectCreateOptions{Name: "p", Workflow: "exclusive"})
		if err != nil {
			t.Fatal(err)
		}
		req.ProductID = 3
		data, _ := json.Marshal(req)
		var got map[string]interface{}
		json.Unmarshal(data, &got)
		want := map[string]interface{}{
			"name": "p", "description": "", "product_id": float64(3),
			"editing_mode": "exclusive", "deployable": false,
			"instance_default_role": "No Access", "require_approval": true,
			"tags": []interface{}{},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("import infers the provider from well-known hosts", func(t *testing.T) {
		for url, provider := range map[string]string{
			"https://github.com/me/MyPkg.jl":     "github",
			"https://GitLab.com/me/MyPkg.jl.git": "gitlab",
			"https://bitbucket.org/me/repo":      "bitbucket",
		} {
			got, err := buildAddProjectRequest(projectCreateOptions{Name: "p", ImportURL: url})
			if err != nil {
				t.Fatalf("%s: %v", url, err)
			}
			if got.GitUpstreamURL != url || got.GitProviderType != provider {
				t.Errorf("%s: got url=%q provider=%q, want provider %q", url, got.GitUpstreamURL, got.GitProviderType, provider)
			}
		}
	})

	t.Run("import from another host takes --provider", func(t *testing.T) {
		got, err := buildAddProjectRequest(projectCreateOptions{Name: "p", ImportURL: "https://git.example.com/me/repo", Provider: "GitLab"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GitProviderType != "gitlab" {
			t.Errorf("provider = %q, want gitlab", got.GitProviderType)
		}
	})

	for _, tc := range []struct {
		desc string
		opts projectCreateOptions
		want string
	}{
		{"empty name", projectCreateOptions{Name: "  "}, "must not be empty"},
		{"unknown workflow", projectCreateOptions{Name: "p", Workflow: "branch"}, "unknown --workflow"},
		{"provider without import", projectCreateOptions{Name: "p", Provider: "github"}, "only applies with --import"},
		{"not a URL", projectCreateOptions{Name: "p", ImportURL: "git@github.com:me/repo.git"}, "invalid --import URL"},
		{"unknown host", projectCreateOptions{Name: "p", ImportURL: "https://git.example.com/me/repo"}, "pass --provider"},
		{"unknown provider", projectCreateOptions{Name: "p", ImportURL: "https://git.example.com/me/repo", Provider: "gitea"}, "unknown --provider"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := buildAddProjectRequest(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestResolveProduct(t *testing.T) {
	products := []projectProduct{
		{ID: 3, Name: "standard-interactive", DisplayName: "Julia IDE"},
		{ID: 7, Name: "rstudio", DisplayName: "RStudio"},
	}
	for flag, wantID := range map[string]int64{
		"":          3,
		"7":         7,
		"julia ide": 3,
		"RSTUDIO":   7,
	} {
		got, err := resolveProduct(products, flag)
		if err != nil {
			t.Errorf("%q: %v", flag, err)
		} else if got.ID != wantID {
			t.Errorf("%q: got id %d, want %d", flag, got.ID, wantID)
		}
	}

	if _, err := resolveProduct(products, "matlab"); err == nil || !strings.Contains(err.Error(), "RStudio (rstudio, id 7)") {
		t.Errorf("unknown product: err = %v, want it to list the available products", err)
	}
	if _, err := resolveProduct(nil, ""); err == nil {
		t.Error("no products: want an error")
	}
}

type fakeResponse struct {
	status int
	body   string
}

// fakeProjectAPI serves canned responses by path and records each request.
type fakeProjectAPI struct {
	responses map[string]fakeResponse
	paths     []string
	bodies    []string
	auth      []string
}

func (f *fakeProjectAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.paths = append(f.paths, r.Method+" "+r.URL.Path)
	f.bodies = append(f.bodies, string(body))
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	resp, ok := f.responses[r.URL.Path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(resp.status)
	w.Write([]byte(resp.body))
}

func newFakeProjectAPI(responses map[string]fakeResponse) (*fakeProjectAPI, *httptest.Server) {
	f := &fakeProjectAPI{responses: responses}
	return f, httptest.NewServer(f)
}

func TestAddAndInitializeProject(t *testing.T) {
	const id = "7b1c10b6-cdfc-480d-8595-8ef3a123f66b"
	f, srv := newFakeProjectAPI(map[string]fakeResponse{
		"/api/v1/projects/add":          {200, `{"project_id":"` + id + `"}`},
		"/api/v1/projects/create/" + id: {200, `{}`},
		"/api/v1/projects/pull/" + id:   {409, `{"success":false,"message":"nothing to pull"}`},
	})
	defer srv.Close()

	req, _ := buildAddProjectRequest(projectCreateOptions{Name: "p"})
	got, err := addProject(srv.Client(), srv.URL, "tok", req)
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Errorf("project id = %q, want %q", got, id)
	}
	if err := initializeProject(srv.Client(), srv.URL, "tok", got); err != nil {
		t.Fatalf("a 409 from pull should be fine: %v", err)
	}

	wantPaths := []string{
		"POST /api/v1/projects/add",
		"POST /api/v1/projects/create/" + id,
		"POST /api/v1/projects/pull/" + id,
	}
	if !reflect.DeepEqual(f.paths, wantPaths) {
		t.Errorf("requests = %v, want %v", f.paths, wantPaths)
	}
	for i, a := range f.auth {
		if a != "Bearer tok" {
			t.Errorf("request %d auth = %q", i, a)
		}
	}
	if !strings.Contains(f.bodies[0], `"name":"p"`) || f.bodies[1] != "" {
		t.Errorf("bodies = %q", f.bodies)
	}
}

func TestProjectAPIErrors(t *testing.T) {
	const id = "abc"
	for _, tc := range []struct {
		desc string
		path string
		resp fakeResponse
		run  func(*httptest.Server) error
		want string
	}{
		{
			"duplicate name shows the server's message", "/api/v1/projects/add",
			fakeResponse{400, `{"success":false,"message":"A project of this name already exists."}`},
			func(srv *httptest.Server) error {
				_, err := addProject(srv.Client(), srv.URL, "tok", AddProjectRequest{Name: "p"})
				return err
			},
			"A project of this name already exists.",
		},
		{
			"add without a project id", "/api/v1/projects/add",
			fakeResponse{200, `{}`},
			func(srv *httptest.Server) error {
				_, err := addProject(srv.Client(), srv.URL, "tok", AddProjectRequest{Name: "p"})
				return err
			},
			"no project_id",
		},
		{
			"import without provider authorization", "/api/v1/projects/create/" + id,
			fakeResponse{400, `{"success":false,"message":"NOTOKEN"}`},
			func(srv *httptest.Server) error { return initializeProject(srv.Client(), srv.URL, "tok", id) },
			"not authorized with your git provider",
		},
		{
			"setup failure", "/api/v1/projects/create/" + id,
			fakeResponse{500, "boom"},
			func(srv *httptest.Server) error { return initializeProject(srv.Client(), srv.URL, "tok", id) },
			"status 500",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, srv := newFakeProjectAPI(map[string]fakeResponse{tc.path: tc.resp})
			defer srv.Close()
			err := tc.run(srv)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
