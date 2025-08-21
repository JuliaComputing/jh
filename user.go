package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type UserInfo struct {
	ID                  int64       `json:"id"`
	Name                string      `json:"name"`
	FirstName           string      `json:"firstname"`
	Username            string      `json:"username"`
	Emails              []UserEmail `json:"emails"`
	Groups              []UserGroup `json:"groups"`
	Roles               []UserRole  `json:"roles"`
	AcceptedTOS         bool        `json:"accepted_tos"`
	SurveySubmittedTime *string     `json:"survey_submitted_time"`
}

type UserEmail struct {
	Email string `json:"email"`
}

type UserGroup struct {
	ID    int64            `json:"id"`
	Group UserGroupDetails `json:"group"`
}

type UserGroupDetails struct {
	Name    string `json:"name"`
	GroupID int64  `json:"group_id"`
}

type UserRole struct {
	Role UserRoleDetails `json:"role"`
}

type UserRoleDetails struct {
	Description string `json:"description"`
	ID          int64  `json:"id"`
	Name        string `json:"name"`
}

type UserInfoRequest struct {
	OperationName string                 `json:"operationName"`
	Query         string                 `json:"query"`
	Variables     map[string]interface{} `json:"variables"`
}

type UserInfoResponse struct {
	Data struct {
		Users []UserInfo `json:"users"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func getUserInfo(server string) (*UserInfo, error) {
	token, err := ensureValidToken()
	if err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// GraphQL query from userinfo.gql
	query := `query UserInfo {
  users(limit: 1) {
    id
    name
    firstname
    emails {
      email
    }
    groups: user_groups {
      id: group_id
      group {
        name
        group_id
      }
    }
    username
    roles {
      role {
        description
        id
        name
      }
    }
    accepted_tos
    survey_submitted_time
  }
}`

	// Create GraphQL request
	graphqlReq := UserInfoRequest{
		OperationName: "UserInfo",
		Query:         query,
		Variables:     map[string]interface{}{},
	}

	jsonData, err := json.Marshal(graphqlReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal GraphQL request: %w", err)
	}

	url := fmt.Sprintf("https://%s/v1/graphql", server)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.IDToken))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Hasura-Role", "jhuser")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GraphQL request failed (status %d): %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var response UserInfoResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Check for GraphQL errors
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("GraphQL errors: %v", response.Errors)
	}

	if len(response.Data.Users) == 0 {
		return nil, fmt.Errorf("no user information found")
	}

	return &response.Data.Users[0], nil
}

func showUserInfo(server string) error {
	userInfo, err := getUserInfo(server)
	if err != nil {
		return err
	}

	fmt.Printf("User Information:\n\n")
	fmt.Printf("ID: %d\n", userInfo.ID)
	fmt.Printf("Name: %s\n", userInfo.Name)
	fmt.Printf("First Name: %s\n", userInfo.FirstName)
	fmt.Printf("Username: %s\n", userInfo.Username)
	fmt.Printf("Accepted Terms of Service: %t\n", userInfo.AcceptedTOS)

	if userInfo.SurveySubmittedTime != nil {
		fmt.Printf("Survey Submitted: %s\n", *userInfo.SurveySubmittedTime)
	}

	// Show emails
	if len(userInfo.Emails) > 0 {
		fmt.Printf("\nEmails:\n")
		for _, email := range userInfo.Emails {
			fmt.Printf("  - %s\n", email.Email)
		}
	}

	// Show groups
	if len(userInfo.Groups) > 0 {
		fmt.Printf("\nGroups:\n")
		for _, group := range userInfo.Groups {
			fmt.Printf("  - %s (ID: %d)\n", group.Group.Name, group.Group.GroupID)
		}
	}

	// Show roles
	if len(userInfo.Roles) > 0 {
		fmt.Printf("\nRoles:\n")
		for _, role := range userInfo.Roles {
			fmt.Printf("  - %s: %s\n", role.Role.Name, role.Role.Description)
		}
	}

	return nil
}
