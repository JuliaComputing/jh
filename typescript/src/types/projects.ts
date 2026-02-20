/**
 * Project-related types
 * Migrated from projects.go
 */

export interface Project {
  id: string;
  project_id: string;
  name: string;
  owner: ProjectOwner;
  created_at: string;
  product_id: number;
  finished: boolean;
  is_archived: boolean;
  instance_default_role: string;
  deployable: boolean;
  project_deployments_aggregate: {
    aggregate: {
      count: number;
    };
  };
  running_deployments: {
    aggregate: {
      count: number;
    };
  };
  pending_deployments: {
    aggregate: {
      count: number;
    };
  };
  resources: Resource[];
  product: Product;
  visibility: string;
  description: string;
  users: User[];
  groups: Group[];
  tags: string[];
  userRole: {
    aggregate: {
      max: {
        assigned_role: string;
      };
    };
  };
  is_simple_mode: boolean;
  projects_current_editor_user_id: {
    name: string;
    id: number;
  };
}

export interface ProjectOwner {
  username: string;
  name: string;
}

export interface Resource {
  sorting_order: number | null;
  instance_default_role: string;
  giturl: string;
  name: string;
  resource_id: string;
  resource_type: string;
}

export interface Product {
  id: number;
  displayName: string;
  name: string;
}

export interface User {
  user: {
    name: string;
  };
  id: number;
  assigned_role: string;
}

export interface Group {
  group: {
    name: string;
    group_id: number;
  };
  id: number;
  group_id: number;
  project_id: string;
  assigned_role: string;
}

export interface GraphQLRequest {
  operationName: string;
  query: string;
  variables: Record<string, any>;
}

export interface ProjectsResponse {
  data: {
    projects: Project[];
    aggregate: {
      aggregate: {
        count: number;
      };
    };
  };
  errors?: Array<{
    message: string;
  }>;
}
