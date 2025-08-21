package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

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
	Resources   []Resource `json:"resources"`
	Product     Product    `json:"product"`
	Visibility  string     `json:"visibility"`
	Description string     `json:"description"`
	Users       []User     `json:"users"`
	Groups      []Group    `json:"groups"`
	Tags        []string   `json:"tags"`
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

type Resource struct {
	SortingOrder        *int   `json:"sorting_order"`
	InstanceDefaultRole string `json:"instance_default_role"`
	GitURL              string `json:"giturl"`
	Name                string `json:"name"`
	ResourceID          string `json:"resource_id"`
	ResourceType        string `json:"resource_type"`
}

type Product struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"displayName"`
	Name        string `json:"name"`
}

type User struct {
	User struct {
		Name string `json:"name"`
	} `json:"user"`
	ID           int64  `json:"id"`
	AssignedRole string `json:"assigned_role"`
}

type Group struct {
	Group struct {
		Name    string `json:"name"`
		GroupID int64  `json:"group_id"`
	} `json:"group"`
	ID           int64  `json:"id"`
	GroupID      int64  `json:"group_id"`
	ProjectID    string `json:"project_id"`
	AssignedRole string `json:"assigned_role"`
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

func listProjects(server string, userFilter string, userFilterProvided bool) error {
	token, err := ensureValidToken()
	if err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// Get user info to get the user ID
	userInfo, err := getUserInfo(server)
	if err != nil {
		return fmt.Errorf("failed to get user info: %w", err)
	}

	// Read the GraphQL query from projects.gql
	query := `query Projects(
  $limit: Int
    $offset: Int
      $orderBy: [projects_order_by!]
        $ownerId: bigint
          $filter: projects_bool_exp
            ) {
              aggregate: projects_aggregate(where: $filter) {
                aggregate {
                  count
                }
              }
              projects(limit: $limit, offset: $offset, order_by: $orderBy, where: $filter) {
                id: project_id
                project_id
                name
                owner {
                  username
                  name
                }
                created_at
                product_id
                finished
                is_archived
                instance_default_role
                deployable
                project_deployments_aggregate {
                  aggregate {
                    count
                  }
                }
                running_deployments: project_deployments_aggregate(
                  where: {
                    status: { _eq: "JobQueued" }
                    job: { status: { _eq: "Running" } }
                  }
                ) {
                  aggregate {
                    count
                  }
                }
                pending_deployments: project_deployments_aggregate(
                  where: {
                    status: { _eq: "JobQueued" }
                    job: { status: { _in: ["SubmitInitialized", "Submitted", "Pending"] } }
                  }
                ) {
                  aggregate {
                    count
                  }
                }
                resources(order_by: [{ sorting_order: asc_nulls_last }]) {
                  sorting_order
                  instance_default_role
                  giturl
                  name
                  resource_id
                  resource_type
                }
                product {
                  id
                  displayName: display_name
                  name
                }
                visibility
                description
                users: groups(where: { group_id: { _is_null: true } }) {
                  user {
                    name
                  }
                  id
                  assigned_role
                }
                groups(where: { group_id: { _is_null: false } }) {
                  group {
                    name
                    group_id
                  }
                  id: group_id
                  group_id
                  project_id
                  assigned_role
                }
                tags
                userRole: access_control_users_aggregate(
                  where: { user_id: { _eq: $ownerId } }
                ) {
                  aggregate {
                    max {
                      assigned_role
                    }
                  }
                }
                is_simple_mode
                projects_current_editor_user_id {
                  name
                  id
                }
              }
            }`

	// Create GraphQL request
	graphqlReq := GraphQLRequest{
		OperationName: "Projects",
		Query:         query,
		Variables: map[string]interface{}{
			"ownerId": userInfo.ID,
		},
	}

	jsonData, err := json.Marshal(graphqlReq)
	if err != nil {
		return fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	url := fmt.Sprintf("https://%s/v1/graphql", server)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Hasura-Role", "jhuser")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GraphQL request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var response ProjectsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for GraphQL errors
	if len(response.Errors) > 0 {
		return fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	projects := response.Data.Projects

	// Apply user filtering if requested
	if userFilterProvided {
		var filteredProjects []Project

		if userFilter == "" {
			// Show only current user's projects
			for _, project := range projects {
				if project.Owner.Username == userInfo.Username {
					filteredProjects = append(filteredProjects, project)
				}
			}
		} else {
			// Show projects from specified user
			for _, project := range projects {
				if strings.EqualFold(project.Owner.Username, userFilter) {
					filteredProjects = append(filteredProjects, project)
				}
			}
		}

		projects = filteredProjects
	}

	if len(projects) == 0 {
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

	if userFilterProvided {
		if userFilter == "" {
			fmt.Printf("Found %d project(s) for your user:\n\n", len(projects))
		} else {
			fmt.Printf("Found %d project(s) for user '%s':\n\n", len(projects), userFilter)
		}
	} else {
		fmt.Printf("Found %d project(s):\n\n", len(projects))
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

		// Show resources
		if len(project.Resources) > 0 {
			fmt.Printf("Resources:\n")
			for _, resource := range project.Resources {
				fmt.Printf("  - %s (%s)\n", resource.Name, resource.ResourceType)
				if resource.GitURL != "" {
					fmt.Printf("    Git URL: %s\n", resource.GitURL)
				}
			}
		}

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
