package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The server answers only allowlisted GraphQL documents and rejects any other
// text with "query is not allowed", so these files must stay, field for field,
// documents the server allowlists. Page and filter only through the variables
// they all declare ($limit, $offset, $orderBy, $ownerId, $filter); never edit
// the text.
//
// projects.gql and projects_pre264.gql are the JuliaHub web UI's Projects
// document (frontend/src/components/Projects/queries/projects.gql) from 26.4 on
// and from 6.9 through 26.3 (without `editing_mode` and
// `requires_publish_request`).
//
// projects_juliasim.gql is JuliaSim's Projects document
// (graphqlsrvr/external-queries/juliasim-projects.gql), allowlisted from 6.7 on.
// Its `resources` field costs the server about a second per row, so only the
// owner/name lookup of `jh clone`, which matches a row or two, falls back to it;
// project listings never do.
//
//go:embed projects.gql
var projectsQuery string

//go:embed projects_pre264.gql
var projectsPre264Query string

//go:embed projects_juliasim.gql
var projectsJuliaSimQuery string

type projectsDocument struct {
	name  string
	query string
}

// listDocuments are tried in order for project listings, lookupDocuments for
// the `jh clone` owner/name lookup.
var (
	listDocuments = []projectsDocument{
		{"26.4+", projectsQuery},
		{"6.9–26.3", projectsPre264Query},
	}
	lookupDocuments = append(listDocuments[:len(listDocuments):len(listDocuments)],
		projectsDocument{"JuliaSim", projectsJuliaSimQuery})
)

// projectLookupLimit bounds the owner/name lookup. The filter matches only the
// case variants of one name, and the JuliaSim document is slow per row.
const projectLookupLimit = 10

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
	Errors []graphQLError `json:"errors"`
}

type graphQLError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

// String prints only the message, so errors read "GraphQL errors: [{message}]".
func (e graphQLError) String() string { return "{" + e.Message + "}" }

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
	return ownerUsernameFilter(userFilter)
}

// ownerUsernameFilter matches projects whose owner's username equals username,
// case-insensitively and literally (LIKE metacharacters escaped).
func ownerUsernameFilter(username string) map[string]interface{} {
	return map[string]interface{}{
		"owner": map[string]interface{}{
			"username": map[string]interface{}{"_ilike": escapeLikePattern(username)},
		},
	}
}

// fetchProjectsPage requests one page of projects matching filter. It returns
// the page's projects and the total number of matching projects on the server.
func fetchProjectsPage(server string, token *StoredToken, ownerID int64, filter map[string]interface{}, page int) ([]Project, int, error) {
	return fetchProjectsPageWith(func(req GraphQLRequest) ([]byte, error) {
		return executeGraphQL(server, token, req)
	}, ownerID, filter, page)
}

// fetchProjectsPageWith is fetchProjectsPage with the GraphQL transport passed
// in.
func fetchProjectsPageWith(execute func(GraphQLRequest) ([]byte, error), ownerID int64, filter map[string]interface{}, page int) ([]Project, int, error) {
	return fetchProjectsWith(execute, listDocuments, ownerID, filter, projectsPageSize, (page-1)*projectsPageSize)
}

// lookupProjectsWith runs the `jh clone` owner/name lookup filter with the
// GraphQL transport passed in.
func lookupProjectsWith(execute func(GraphQLRequest) ([]byte, error), ownerID int64, filter map[string]interface{}) ([]Project, error) {
	projects, _, err := fetchProjectsWith(execute, lookupDocuments, ownerID, filter, projectLookupLimit, 0)
	return projects, err
}

// fetchProjectsWith sends docs in order until the server accepts one, and moves
// to the next only when the server rejects the document itself (see
// queryProjects). When none is accepted, every document's error is reported.
func fetchProjectsWith(execute func(GraphQLRequest) ([]byte, error), docs []projectsDocument, ownerID int64, filter map[string]interface{}, limit, offset int) ([]Project, int, error) {
	var errs []error
	for _, d := range docs {
		projects, total, rejected, err := queryProjects(execute, d.query, ownerID, filter, limit, offset)
		if err == nil {
			return projects, total, nil
		}
		if len(errs) > 0 {
			err = fmt.Errorf("with the %s document: %w", d.name, err)
		}
		errs = append(errs, err)
		if !rejected {
			break
		}
	}
	if len(errs) == 1 {
		return nil, 0, errs[0]
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return nil, 0, errors.New(strings.Join(msgs, "; "))
}

// queryProjects sends one Projects document. rejected reports that the server
// refused the document itself rather than failed to run it: it is not on the
// allowlist ("query is not allowed"), or, on a server running without an
// allowlist, it selects a project field that server's schema lacks ("field ...
// not found in type: 'projects'"). Only then is another document worth trying.
func queryProjects(execute func(GraphQLRequest) ([]byte, error), query string, ownerID int64, filter map[string]interface{}, limit, offset int) (projects []Project, total int, rejected bool, err error) {
	body, err := execute(GraphQLRequest{
		OperationName: "Projects",
		Query:         query,
		Variables: map[string]interface{}{
			"ownerId": ownerID,
			"filter":  filter,
			"limit":   limit,
			"offset":  offset,
			"orderBy": map[string]interface{}{"created_at": "desc_nulls_last"},
		},
	})
	if err != nil {
		return nil, 0, false, err
	}

	var response ProjectsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, 0, false, fmt.Errorf("failed to parse response: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, 0, response.documentRejected(), fmt.Errorf("GraphQL errors: %v", response.Errors)
	}
	return response.Data.Projects, response.Data.Aggregate.Aggregate.Count, false, nil
}

// documentRejected reports whether the server refused the document itself
// (see queryProjects) rather than failing to run it.
func (r ProjectsResponse) documentRejected() bool {
	for _, e := range r.Errors {
		if strings.Contains(strings.ToLower(e.Message), "query is not allowed") {
			return true
		}
		if e.Extensions.Code == "validation-failed" && strings.HasSuffix(e.Message, "not found in type: 'projects'") {
			return true
		}
	}
	return false
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
