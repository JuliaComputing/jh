import {
  Project,
  GraphQLRequest,
  ProjectsResponse,
} from '../types/projects';
import { AuthService } from './auth';
import { UserService } from './user';
import { IFileSystem } from '../types/filesystem';

/**
 * Projects service for managing JuliaHub projects
 * Migrated from projects.go and git.go (project lookup parts)
 */
export class ProjectsService {
  private authService: AuthService;
  private userService: UserService;

  constructor(fs: IFileSystem) {
    this.authService = new AuthService(fs);
    this.userService = new UserService(fs);
  }

  /**
   * Execute a GraphQL projects query
   */
  private async executeProjectsQuery(
    server: string,
    query: string,
    userID: number
  ): Promise<ProjectsResponse> {
    const token = await this.authService.ensureValidToken();

    const graphqlReq: GraphQLRequest = {
      operationName: 'Projects',
      query: query,
      variables: {
        ownerId: userID,
      },
    };

    const url = `https://${server}/v1/graphql`;
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token.idToken}`,
        'Content-Type': 'application/json',
        Accept: 'application/json',
        'X-Hasura-Role': 'jhuser',
      },
      body: JSON.stringify(graphqlReq),
    });

    if (!resp.ok) {
      const errorText = await resp.text();
      throw new Error(
        `GraphQL request failed (status ${resp.status}): ${errorText}`
      );
    }

    const response = (await resp.json()) as ProjectsResponse;

    // Check for GraphQL errors
    if (response.errors && response.errors.length > 0) {
      throw new Error(`GraphQL errors: ${JSON.stringify(response.errors)}`);
    }

    return response;
  }

  /**
   * List all projects with optional user filtering
   */
  async listProjects(
    server: string,
    userFilter?: string,
    userFilterProvided: boolean = false
  ): Promise<string> {
    // Get user info to get the user ID
    const userInfo = await this.userService.getUserInfo(server);

    // GraphQL query
    const query = `query Projects(
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
            }`;

    const response = await this.executeProjectsQuery(server, query, userInfo.id);
    let projects = response.data.projects;

    // Apply user filtering if requested
    if (userFilterProvided) {
      if (!userFilter) {
        // Show only current user's projects
        projects = projects.filter((p) => p.owner.username === userInfo.username);
      } else {
        // Show projects from specified user
        projects = projects.filter(
          (p) => p.owner.username.toLowerCase() === userFilter.toLowerCase()
        );
      }
    }

    if (projects.length === 0) {
      if (userFilterProvided) {
        if (!userFilter) {
          return 'No projects found for your user';
        } else {
          return `No projects found for user '${userFilter}'`;
        }
      } else {
        return 'No projects found';
      }
    }

    let output = '';
    if (userFilterProvided) {
      if (!userFilter) {
        output += `Found ${projects.length} project(s) for your user:\n\n`;
      } else {
        output += `Found ${projects.length} project(s) for user '${userFilter}':\n\n`;
      }
    } else {
      output += `Found ${projects.length} project(s):\n\n`;
    }

    for (const project of projects) {
      output += this.formatProject(project);
      output += '\n';
    }

    return output;
  }

  /**
   * Format a single project for display
   */
  private formatProject(project: Project): string {
    let output = '';
    output += `ID: ${project.id}\n`;
    output += `Name: ${project.name}\n`;
    output += `Owner: ${project.owner.username} (${project.owner.name})\n`;

    if (project.description) {
      output += `Description: ${project.description}\n`;
    }

    output += `Visibility: ${project.visibility}\n`;
    output += `Product: ${project.product.displayName}\n`;
    output += `Created: ${project.created_at}\n`;
    output += `Finished: ${project.finished}\n`;
    output += `Archived: ${project.is_archived}\n`;
    output += `Deployable: ${project.deployable}\n`;

    // Show deployment counts
    const totalDeployments = project.project_deployments_aggregate.aggregate.count;
    const runningDeployments = project.running_deployments.aggregate.count;
    const pendingDeployments = project.pending_deployments.aggregate.count;
    output += `Deployments: ${totalDeployments} total, ${runningDeployments} running, ${pendingDeployments} pending\n`;

    // Show resources
    if (project.resources.length > 0) {
      output += 'Resources:\n';
      for (const resource of project.resources) {
        output += `  - ${resource.name} (${resource.resource_type})\n`;
        if (resource.giturl) {
          output += `    Git URL: ${resource.giturl}\n`;
        }
      }
    }

    // Show tags
    if (project.tags.length > 0) {
      output += `Tags: ${project.tags.join(', ')}\n`;
    }

    // Show user role
    if (project.userRole.aggregate.max.assigned_role) {
      output += `Your Role: ${project.userRole.aggregate.max.assigned_role}\n`;
    }

    return output;
  }

  /**
   * Find a project by username and project name
   */
  async findProjectByUserAndName(
    server: string,
    username: string,
    projectName: string
  ): Promise<string> {
    // Get user info to get the user ID
    const userInfo = await this.userService.getUserInfo(server);

    // Use the same GraphQL query as listProjects
    const query = `query Projects(
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
            }`;

    const response = await this.executeProjectsQuery(server, query, userInfo.id);

    // Search for the project
    const matchedProject = response.data.projects.find(
      (project) =>
        project.owner.username.toLowerCase() === username.toLowerCase() &&
        project.name.toLowerCase() === projectName.toLowerCase()
    );

    if (!matchedProject) {
      throw new Error(`Project '${projectName}' not found for user '${username}'`);
    }

    console.log(
      `Found project: ${matchedProject.name} by ${matchedProject.owner.username} (ID: ${matchedProject.id})`
    );
    return matchedProject.id;
  }
}
