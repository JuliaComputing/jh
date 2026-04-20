/**
 * User-related types
 * Migrated from user.go
 */

export interface UserInfo {
  id: number;
  name: string;
  firstname: string;
  username: string;
  emails: UserEmail[];
  groups: UserGroup[];
  roles: UserRole[];
  accepted_tos: boolean;
  survey_submitted_time: string | null;
}

export interface UserEmail {
  email: string;
}

export interface UserGroup {
  id: number;
  group: UserGroupDetails;
}

export interface UserGroupDetails {
  name: string;
  group_id: number;
}

export interface UserRole {
  role: UserRoleDetails;
}

export interface UserRoleDetails {
  description: string;
  id: number;
  name: string;
}

export interface UserInfoRequest {
  operationName: string;
  query: string;
  variables: Record<string, any>;
}

export interface UserInfoResponse {
  data: {
    users: UserInfo[];
  };
  errors?: Array<{
    message: string;
  }>;
}
