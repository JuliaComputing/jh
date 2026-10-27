package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed projects.gql
var projectsQuery string

//go:embed products.gql
var productsQuery string

type Project struct {
	ID                          string       `json:"id"`
	ProjectID                   string       `json:"project_id"`
	Name                        string       `json:"name"`
	Owner                       ProjectOwner `json:"owner"`
	CreatedAt                   string       `json:"created_at"`
	ProductID                   int64        `json:"product_id"`
	Finished                    bool         `json:"finished"`
	IsArchived                  bool         `json:"is_archived"`
	InstanceDefaultRole         string       `json:"instance_default_role"`
	Deployable                  bool         `json:"deployable"`
	ProjectDeploymentsAggregate struct {
		Aggregate struct {
			Count int `json:"count"`
		} `json:"aggregate"`
	} `json:"project_deployments_aggregate"`
	RunningDeployments struct {
		Aggregate struct {
			Count int `json:"count"`
		} `json:"aggregate"`
	} `json:"running_deployments"`
	PendingDeployments struct {
		Aggregate struct {
			Count int `json:"count"`
		} `json:"aggregate"`
	} `json:"pending_deployments"`
	Product     Product  `json:"product"`
	Visibility  string   `json:"visibility"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	UserRole    struct {
		Aggregate struct {
			Max struct {
				AssignedRole string `json:"assigned_role"`
			} `json:"max"`
		} `json:"aggregate"`
	} `json:"userRole"`
	IsSimpleMode                bool `json:"is_simple_mode"`
	ProjectsCurrentEditorUserID struct {
		Name string `json:"name"`
		ID   int64  `json:"id"`
	} `json:"projects_current_editor_user_id"`
}

type ProjectOwner struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Product struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
}

type GraphQLRequest struct {
	OperationName string                 `json:"operationName"`
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
}

type ProjectsResponse struct {
	Data struct {
		Projects  []Project `json:"projects"`
		Aggregate struct {
			Aggregate struct {
				Count int `json:"count"`
			} `json:"aggregate"`
		} `json:"aggregate"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// projectsPageSize is how many projects are fetched per GraphQL request.
// The Projects query is expensive per row on the server (several aggregates
// and permission checks per project), so it is always paginated and filtered
// server-side; never ask Hasura for the whole projects table.
const projectsPageSize = 100

// escapeLikePattern escapes the LIKE/ILIKE metacharacters in s so that it
// matches literally when used as an _ilike pattern.
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// buildProjectsFilter returns the projects_bool_exp to send to Hasura.
// Filtering happens server-side so the caller only receives (and the server
// only computes) the projects that will actually be shown.
func buildProjectsFilter(userFilter string, userFilterProvided bool, currentUserID int64) map[string]interface{} {
	if !userFilterProvided {
		return map[string]interface{}{}
	}
	if userFilter == "" {
		return map[string]interface{}{
			"owner_id": map[string]interface{}{"_eq": currentUserID},
		}
	}
	// Case-insensitive exact match on the owner's username (was EqualFold client-side).
	return map[string]interface{}{
		"owner": map[string]interface{}{
			"username": map[string]interface{}{"_ilike": escapeLikePattern(userFilter)},
		},
	}
}

// fetchProjectsPage requests one page of projects matching filter. It returns
// the page's projects and the total number of matching projects on the server.
func fetchProjectsPage(server string, token *StoredToken, ownerID int64, filter map[string]interface{}, page int) ([]Project, int, error) {
	body, err := executeGraphQL(server, token, GraphQLRequest{
		OperationName: "Projects",
		Query:         projectsQuery,
		Variables: map[string]interface{}{
			"ownerId": ownerID,
			"filter":  filter,
			"limit":   projectsPageSize,
			"offset":  (page - 1) * projectsPageSize,
			"orderBy": map[string]interface{}{"created_at": "desc_nulls_last"},
		},
	})
	if err != nil {
		return nil, 0, err
	}

	var response ProjectsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, 0, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, 0, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}
	return response.Data.Projects, response.Data.Aggregate.Aggregate.Count, nil
}

// totalPages is how many pages of projectsPageSize the given total spans.
func totalPages(total int) int {
	if total <= 0 {
		return 0
	}
	return (total + projectsPageSize - 1) / projectsPageSize
}

func listProjects(server string, userFilter string, userFilterProvided bool, page int) error {
	if page < 1 {
		return fmt.Errorf("--page must be 1 or greater, got %d", page)
	}

	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Get user info to get the user ID
	userInfo, err := getUserInfo(server)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	filter := buildProjectsFilter(userFilter, userFilterProvided, userInfo.ID)
	projects, total, err := fetchProjectsPage(server, token, userInfo.ID, filter, page)
	if err != nil {
		return err
	}

	if total == 0 {
		if userFilterProvided {
			if userFilter == "" {
				fmt.Println("No projects found for your user")
			} else {
				fmt.Printf("No projects found for user '%s'\n", userFilter)
			}
		} else {
			fmt.Println("No projects found")
		}
		return nil
	}

	pages := totalPages(total)
	if len(projects) == 0 {
		fmt.Printf("Page %d is empty: %d project(s) found, %d page(s) of %d\n", page, total, pages, projectsPageSize)
		return nil
	}

	if userFilterProvided {
		if userFilter == "" {
			fmt.Printf("Found %d project(s) for your user:\n\n", total)
		} else {
			fmt.Printf("Found %d project(s) for user '%s':\n\n", total, userFilter)
		}
	} else {
		fmt.Printf("Found %d project(s):\n\n", total)
	}
	if pages > 1 {
		fmt.Printf("Showing page %d of %d (%d per page, most recently created first; use --page to see more)\n\n", page, pages, projectsPageSize)
	}

	for _, project := range projects {
		fmt.Printf("ID: %s\n", project.ID)
		fmt.Printf("Name: %s\n", project.Name)
		fmt.Printf("Owner: %s (%s)\n", project.Owner.Username, project.Owner.Name)
		if project.Description != "" {
			fmt.Printf("Description: %s\n", project.Description)
		}
		fmt.Printf("Visibility: %s\n", project.Visibility)
		fmt.Printf("Product: %s\n", project.Product.DisplayName)
		fmt.Printf("Created: %s\n", project.CreatedAt)
		fmt.Printf("Finished: %t\n", project.Finished)
		fmt.Printf("Archived: %t\n", project.IsArchived)
		fmt.Printf("Deployable: %t\n", project.Deployable)

		// Show deployment counts
		totalDeployments := project.ProjectDeploymentsAggregate.Aggregate.Count
		runningDeployments := project.RunningDeployments.Aggregate.Count
		pendingDeployments := project.PendingDeployments.Aggregate.Count
		fmt.Printf("Deployments: %d total, %d running, %d pending\n",
			totalDeployments, runningDeployments, pendingDeployments)

		// Show tags
		if len(project.Tags) > 0 {
			fmt.Printf("Tags: %v\n", project.Tags)
		}

		// Show user role
		if project.UserRole.Aggregate.Max.AssignedRole != "" {
			fmt.Printf("Your Role: %s\n", project.UserRole.Aggregate.Max.AssignedRole)
		}

		fmt.Println()
	}

	return nil
}

type projectProduct struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

// AddProjectRequest is the body of POST /api/v1/projects/add.
type AddProjectRequest struct {
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	ProductID           int64    `json:"product_id"`
	EditingMode         string   `json:"editing_mode"`
	Deployable          bool     `json:"deployable"`
	InstanceDefaultRole string   `json:"instance_default_role"`
	RequireApproval     bool     `json:"require_approval"`
	Tags                []string `json:"tags"`
	GitUpstreamURL      string   `json:"git_upstream_url,omitempty"`
	GitProviderType     string   `json:"git_provider_type,omitempty"`
}

type projectCreateOptions struct {
	Name        string
	Description string
	Tags        []string
	Product     string
	Exclusive   bool
	ImportURL   string
	Provider    string
}

type createdProject struct {
	ProjectID      string `json:"project_id"`
	Name           string `json:"name,omitempty"`
	Owner          string `json:"owner,omitempty"`
	Product        string `json:"product,omitempty"`
	GitUpstreamURL string `json:"git_upstream_url,omitempty"`
}

var gitProviders = []string{"github", "gitlab", "bitbucket"}

// importURLPattern is the check the web UI applies to import URLs.
var importURLPattern = regexp.MustCompile(`^https?://.+\.\w+`)

// Setting up an imported project clones the upstream within the request.
const projectCreateTimeout = 5 * time.Minute

func gitProviderForHost(host string) string {
	switch strings.ToLower(host) {
	case "github.com":
		return "github"
	case "gitlab.com":
		return "gitlab"
	case "bitbucket.org", "bitbucket.com":
		return "bitbucket"
	}
	return ""
}

// buildAddProjectRequest validates opts and builds the request for a private,
// non-deployable project. ProductID is left for the caller to fill in.
func buildAddProjectRequest(opts projectCreateOptions) (AddProjectRequest, error) {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return AddProjectRequest{}, errors.New("project name must not be empty")
	}
	tags := []string{} // NOT NULL on the server
	for _, t := range opts.Tags {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	editingMode := "collaborative"
	if opts.Exclusive {
		editingMode = "exclusive"
	}
	req := AddProjectRequest{
		Name:                name,
		Description:         opts.Description,
		EditingMode:         editingMode,
		InstanceDefaultRole: "No Access", // the only value JuliaHub.com-mode installs accept
		RequireApproval:     true,
		Tags:                tags,
	}

	if opts.ImportURL == "" {
		if opts.Provider != "" {
			return AddProjectRequest{}, errors.New("--provider only applies with --import")
		}
		return req, nil
	}
	u, err := url.Parse(opts.ImportURL)
	if err != nil || !importURLPattern.MatchString(opts.ImportURL) {
		return AddProjectRequest{}, fmt.Errorf("invalid --import URL %q: expected an http(s) git repository URL", opts.ImportURL)
	}
	provider := strings.ToLower(opts.Provider)
	if provider == "" {
		if provider = gitProviderForHost(u.Hostname()); provider == "" {
			return AddProjectRequest{}, fmt.Errorf("cannot tell the git provider of %s; pass --provider (%s)", u.Hostname(), strings.Join(gitProviders, ", "))
		}
	} else if !slices.Contains(gitProviders, provider) {
		return AddProjectRequest{}, fmt.Errorf("unknown --provider %q; expected one of %s", opts.Provider, strings.Join(gitProviders, ", "))
	}
	req.GitUpstreamURL = opts.ImportURL
	req.GitProviderType = provider
	return req, nil
}

// resolveProduct matches flag against product IDs, names and display names
// (case-insensitively). An empty flag picks the first product, like the web UI.
func resolveProduct(products []projectProduct, flag string) (projectProduct, error) {
	if len(products) == 0 {
		return projectProduct{}, errors.New("this server has no products to create a project for")
	}
	flag = strings.TrimSpace(flag)
	if flag == "" {
		return products[0], nil
	}
	id, idErr := strconv.ParseInt(flag, 10, 64)
	for _, p := range products {
		if (idErr == nil && p.ID == id) || strings.EqualFold(p.Name, flag) || strings.EqualFold(p.DisplayName, flag) {
			return p, nil
		}
	}
	available := make([]string, len(products))
	for i, p := range products {
		available[i] = fmt.Sprintf("%s (%s, id %d)", p.DisplayName, p.Name, p.ID)
	}
	return projectProduct{}, fmt.Errorf("unknown product %q; available: %s", flag, strings.Join(available, "; "))
}

func fetchProjectProducts(server string, token *StoredToken) ([]projectProduct, error) {
	body, err := executeGraphQL(server, token, GraphQLRequest{
		OperationName: "Products",
		Query:         productsQuery,
		Variables:     map[string]interface{}{},
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		Data struct {
			Products []projectProduct `json:"products"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse products: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}
	return response.Data.Products, nil
}

// projectAPIError reports the message from the server's
// {"success": false, "message": ...} error body, or the raw body.
func projectAPIError(status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	var parsed struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Message != "" {
		msg = parsed.Message
	}
	switch msg {
	case "NOTOKEN":
		return errors.New("JuliaHub is not authorized with your git provider; authorize it from the web UI and retry")
	case "NOACCESS":
		return errors.New("the repository does not exist or your git provider account cannot read it")
	case "NOPROVIDER":
		return errors.New("no OAuth provider is configured on this JuliaHub for that git server; ask an administrator to add one")
	}
	return fmt.Errorf("request failed (status %d): %s", status, msg)
}

// postProjectAPI POSTs body as JSON (or nothing, if nil) and returns the
// response body, or an error for any non-2xx status other than allowStatus.
func postProjectAPI(client *http.Client, baseURL, idToken, path string, body interface{}, allowStatus int) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest("POST", baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+idToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if (resp.StatusCode < 200 || resp.StatusCode >= 300) && resp.StatusCode != allowStatus {
		return nil, projectAPIError(resp.StatusCode, respBody)
	}
	return respBody, nil
}

// addProject creates the project's database records and returns its ID. The
// project has no repository until initializeProject runs.
func addProject(client *http.Client, baseURL, idToken string, req AddProjectRequest) (string, error) {
	body, err := postProjectAPI(client, baseURL, idToken, "/api/v1/projects/add", req, 0)
	if err != nil {
		return "", err
	}
	var parsed struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if parsed.ProjectID == "" {
		return "", fmt.Errorf("unexpected response, no project_id: %s", string(body))
	}
	return parsed.ProjectID, nil
}

// initializeProject sets up the repository of an added project, then pulls it
// into the caller's workspace, as the web UI does. A 409 from the pull means
// there was nothing to pull.
func initializeProject(client *http.Client, baseURL, idToken, projectID string) error {
	id := url.PathEscape(projectID)
	if _, err := postProjectAPI(client, baseURL, idToken, "/api/v1/projects/create/"+id, nil, 0); err != nil {
		return err
	}
	_, err := postProjectAPI(client, baseURL, idToken, "/api/v1/projects/pull/"+id, nil, http.StatusConflict)
	return err
}

func initializeProjectOrExplain(client *http.Client, baseURL, idToken, projectID string) error {
	if err := initializeProject(client, baseURL, idToken, projectID); err != nil {
		return fmt.Errorf("project %s exists but its repository could not be set up: %w\nRetry with: jh project create --resume %s", projectID, err, projectID)
	}
	return nil
}

func createProject(server string, opts projectCreateOptions, asJSON bool) error {
	req, err := buildAddProjectRequest(opts)
	if err != nil {
		return err
	}
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	userInfo, err := getUserInfo(server)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}
	products, err := fetchProjectProducts(server, token)
	if err != nil {
		return fmt.Errorf("failed to list products: %w", err)
	}
	product, err := resolveProduct(products, opts.Product)
	if err != nil {
		return err
	}
	req.ProductID = product.ID

	client := &http.Client{Timeout: projectCreateTimeout}
	baseURL := "https://" + server
	id, err := addProject(client, baseURL, token.IDToken, req)
	if err != nil {
		return err
	}
	if err := initializeProjectOrExplain(client, baseURL, token.IDToken, id); err != nil {
		return err
	}

	result := createdProject{
		ProjectID:      id,
		Name:           req.Name,
		Owner:          userInfo.Username,
		Product:        product.DisplayName,
		GitUpstreamURL: req.GitUpstreamURL,
	}
	if asJSON {
		return writeJSON(os.Stdout, result)
	}
	fmt.Println(bold("Created project " + result.Name))
	printField("ID", result.ProjectID)
	printField("Owner", result.Owner)
	printField("Product", result.Product)
	printField("Imported from", result.GitUpstreamURL)
	printField("Clone", "jh clone "+result.Owner+"/"+result.Name)
	return nil
}

// resumeProjectSetup retries initializeProject for a project whose add
// succeeded but whose setup failed.
func resumeProjectSetup(server, projectID string, asJSON bool) error {
	if strings.TrimSpace(projectID) == "" {
		return errors.New("--resume needs a project ID")
	}
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}
	client := &http.Client{Timeout: projectCreateTimeout}
	if err := initializeProjectOrExplain(client, "https://"+server, token.IDToken, projectID); err != nil {
		return err
	}
	if asJSON {
		return writeJSON(os.Stdout, createdProject{ProjectID: projectID})
	}
	fmt.Println(bold("Set up project " + projectID))
	return nil
}
