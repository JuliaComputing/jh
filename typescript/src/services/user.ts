import { UserInfo, UserInfoRequest, UserInfoResponse } from '../types/user';
import { AuthService } from './auth';
import { IFileSystem } from '../types/filesystem';

/**
 * User service for fetching user information
 * Migrated from user.go
 */
export class UserService {
  private authService: AuthService;

  constructor(fs: IFileSystem) {
    this.authService = new AuthService(fs);
  }

  /**
   * Get user information from JuliaHub GraphQL API
   */
  async getUserInfo(server: string): Promise<UserInfo> {
    const token = await this.authService.ensureValidToken();

    // GraphQL query from userinfo.gql
    const query = `query UserInfo {
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
}`;

    const graphqlReq: UserInfoRequest = {
      operationName: 'UserInfo',
      query: query,
      variables: {},
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

    const response = (await resp.json()) as UserInfoResponse;

    // Check for GraphQL errors
    if (response.errors && response.errors.length > 0) {
      throw new Error(`GraphQL errors: ${JSON.stringify(response.errors)}`);
    }

    if (response.data.users.length === 0) {
      throw new Error('No user information found');
    }

    return response.data.users[0];
  }

  /**
   * Format user information for display
   */
  formatUserInfo(userInfo: UserInfo): string {
    let output = 'User Information:\n\n';
    output += `ID: ${userInfo.id}\n`;
    output += `Name: ${userInfo.name}\n`;
    output += `First Name: ${userInfo.firstname}\n`;
    output += `Username: ${userInfo.username}\n`;
    output += `Accepted Terms of Service: ${userInfo.accepted_tos}\n`;

    if (userInfo.survey_submitted_time) {
      output += `Survey Submitted: ${userInfo.survey_submitted_time}\n`;
    }

    // Show emails
    if (userInfo.emails.length > 0) {
      output += '\nEmails:\n';
      for (const email of userInfo.emails) {
        output += `  - ${email.email}\n`;
      }
    }

    // Show groups
    if (userInfo.groups.length > 0) {
      output += '\nGroups:\n';
      for (const group of userInfo.groups) {
        output += `  - ${group.group.name} (ID: ${group.group.group_id})\n`;
      }
    }

    // Show roles
    if (userInfo.roles.length > 0) {
      output += '\nRoles:\n';
      for (const role of userInfo.roles) {
        output += `  - ${role.role.name}: ${role.role.description}\n`;
      }
    }

    return output;
  }
}
