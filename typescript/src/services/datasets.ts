import { Dataset, DatasetDownloadURL } from '../types/datasets';
import { AuthService } from './auth';
import { IFileSystem } from '../types/filesystem';
import * as path from 'path';

/**
 * Datasets service for managing JuliaHub datasets
 * Migrated from datasets.go
 */
export class DatasetsService {
  private authService: AuthService;

  constructor(private fs: IFileSystem) {
    this.authService = new AuthService(fs);
  }

  /**
   * List all datasets
   */
  async listDatasets(server: string): Promise<string> {
    const token = await this.authService.ensureValidToken();
    const url = `https://${server}/datasets`;

    const resp = await fetch(url, {
      headers: {
        Authorization: `Bearer ${token.accessToken}`,
        Accept: 'application/json',
      },
    });

    if (!resp.ok) {
      const errorText = await resp.text();
      throw new Error(`API request failed (status ${resp.status}): ${errorText}`);
    }

    const datasets = (await resp.json()) as Dataset[];

    if (datasets.length === 0) {
      return 'No datasets found';
    }

    let output = `Found ${datasets.length} dataset(s):\n\n`;

    for (const dataset of datasets) {
      output += `ID: ${dataset.id}\n`;
      output += `Name: ${dataset.name}\n`;
      output += `Owner: ${dataset.owner.username} (${dataset.owner.type})\n`;

      if (dataset.description) {
        output += `Description: ${dataset.description}\n`;
      }

      output += `Size: ${dataset.size} bytes\n`;
      output += `Visibility: ${dataset.visibility}\n`;
      output += `Type: ${dataset.type}\n`;
      output += `Version: ${dataset.version}\n`;
      output += `Last Modified: ${dataset.lastModified}\n`;

      if (dataset.tags.length > 0) {
        output += `Tags: ${dataset.tags.join(', ')}\n`;
      }

      if (dataset.license.name) {
        output += `License: ${dataset.license.name}\n`;
      }

      output += '\n';
    }

    return output;
  }

  /**
   * Get all datasets (helper for other operations)
   */
  private async getDatasets(server: string): Promise<Dataset[]> {
    const token = await this.authService.ensureValidToken();
    const url = `https://${server}/datasets`;

    const resp = await fetch(url, {
      headers: {
        Authorization: `Bearer ${token.idToken}`,
        Accept: 'application/json',
      },
    });

    if (!resp.ok) {
      const errorText = await resp.text();
      throw new Error(`API request failed (status ${resp.status}): ${errorText}`);
    }

    return (await resp.json()) as Dataset[];
  }

  /**
   * Resolve dataset identifier (UUID, name, or user/name) to UUID
   */
  async resolveDatasetIdentifier(
    server: string,
    identifier: string
  ): Promise<string> {
    // If identifier contains dashes and looks like a UUID, use it directly
    if (identifier.includes('-') && identifier.length >= 32) {
      return identifier;
    }

    // Parse user/name format
    let targetUser = '';
    let targetName = '';

    if (identifier.includes('/')) {
      const parts = identifier.split('/', 2);
      targetUser = parts[0];
      targetName = parts[1];
    } else {
      targetName = identifier;
    }

    console.log(`Searching for dataset: name='${targetName}', user='${targetUser}'`);

    // Get all datasets and search by name/user
    const datasets = await this.getDatasets(server);
    const matches = datasets.filter((dataset) => {
      const nameMatch = dataset.name === targetName;
      const userMatch = !targetUser || dataset.owner.username === targetUser;
      return nameMatch && userMatch;
    });

    if (matches.length === 0) {
      if (targetUser) {
        throw new Error(
          `No dataset found with name '${targetName}' by user '${targetUser}'`
        );
      } else {
        throw new Error(`No dataset found with name '${targetName}'`);
      }
    }

    if (matches.length > 1) {
      console.log(`Multiple datasets found with name '${targetName}':`);
      for (const match of matches) {
        console.log(`  - ${match.name} by ${match.owner.username} (ID: ${match.id})`);
      }
      throw new Error(
        "Multiple datasets found, please specify user as 'user/name' or use dataset ID"
      );
    }

    const match = matches[0];
    console.log(`Found dataset: ${match.name} by ${match.owner.username} (ID: ${match.id})`);
    return match.id;
  }

  /**
   * Get dataset download URL
   */
  private async getDatasetDownloadURL(
    server: string,
    datasetID: string,
    version: string
  ): Promise<DatasetDownloadURL> {
    const token = await this.authService.ensureValidToken();
    const url = `https://${server}/datasets/${datasetID}/url/v${version}`;

    const resp = await fetch(url, {
      headers: {
        Authorization: `Bearer ${token.idToken}`,
        Accept: 'application/json',
      },
    });

    if (!resp.ok) {
      const errorText = await resp.text();
      throw new Error(`Failed to get download URL (status ${resp.status}): ${errorText}`);
    }

    return (await resp.json()) as DatasetDownloadURL;
  }

  /**
   * Get dataset versions
   */
  private async getDatasetVersions(
    server: string,
    datasetID: string
  ): Promise<Dataset> {
    const datasets = await this.getDatasets(server);
    const dataset = datasets.find((d) => d.id === datasetID);

    if (!dataset) {
      throw new Error(`Dataset with ID ${datasetID} not found`);
    }

    console.log(`DEBUG: Found dataset: ${dataset.name}`);
    console.log(`DEBUG: Dataset versions:`);
    for (let i = 0; i < dataset.versions.length; i++) {
      const version = dataset.versions[i];
      console.log(
        `  [${i}] Version ${version.version}, Size: ${version.size}, Date: ${version.date}, BlobstorePath: ${version.blobstore_path}`
      );
    }

    return dataset;
  }

  /**
   * Download a dataset
   */
  async downloadDataset(
    server: string,
    datasetIdentifier: string,
    version: string,
    localPath: string
  ): Promise<void> {
    const datasetID = await this.resolveDatasetIdentifier(server, datasetIdentifier);

    let versionNumber: string;
    let datasetName = datasetID;

    if (version) {
      // Version was provided, strip the 'v' prefix
      versionNumber = version.replace(/^v/, '');
      console.log(`Using specified version: ${version}`);

      // Get dataset name for filename
      const dataset = await this.getDatasetVersions(server, datasetID);
      datasetName = dataset.name || datasetID;
    } else {
      // No version provided, find the latest version
      const dataset = await this.getDatasetVersions(server, datasetID);

      if (dataset.versions.length === 0) {
        throw new Error('No versions available for dataset');
      }

      // Find the latest version (highest version number)
      const latestVersion = dataset.versions.reduce((latest, current) =>
        current.version > latest.version ? current : latest
      );

      console.log(`DEBUG: Latest version found: ${latestVersion.version}`);
      versionNumber = String(latestVersion.version);
      datasetName = dataset.name || datasetID;
    }

    // Get download URL
    const downloadInfo = await this.getDatasetDownloadURL(
      server,
      datasetID,
      versionNumber
    );

    console.log(`Downloading dataset: ${downloadInfo.dataset}`);
    console.log(`Version: ${downloadInfo.version}`);
    console.log(`Download URL: ${downloadInfo.url}`);

    // Download the file
    const resp = await fetch(downloadInfo.url);

    if (!resp.ok) {
      const errorText = await resp.text();
      throw new Error(`Download failed (status ${resp.status}): ${errorText}`);
    }

    // Determine local file name
    if (!localPath) {
      localPath = downloadInfo.dataset
        ? `${downloadInfo.dataset}.tar.gz`
        : `${datasetName}.tar.gz`;
    }

    // Write file
    const buffer = await resp.arrayBuffer();
    await this.fs.writeFile(localPath, Buffer.from(buffer).toString('binary'), {
      encoding: 'binary' as BufferEncoding,
    });

    console.log(`Successfully downloaded dataset to: ${localPath}`);
  }

  /**
   * Show dataset status
   */
  async statusDataset(
    server: string,
    datasetIdentifier: string,
    version: string
  ): Promise<string> {
    const datasetID = await this.resolveDatasetIdentifier(server, datasetIdentifier);

    let versionNumber: string;

    if (version) {
      versionNumber = version.replace(/^v/, '');
      console.log(`Using specified version: ${version}`);

      const dataset = await this.getDatasetVersions(server, datasetID);
      if (dataset.versions.length === 0) {
        throw new Error('No versions available for dataset');
      }
    } else {
      const dataset = await this.getDatasetVersions(server, datasetID);

      if (dataset.versions.length === 0) {
        throw new Error('No versions available for dataset');
      }

      const latestVersion = dataset.versions.reduce((latest, current) =>
        current.version > latest.version ? current : latest
      );

      console.log(`DEBUG: Latest version found: ${latestVersion.version}`);
      versionNumber = String(latestVersion.version);
    }

    // Get download URL (but don't download)
    const downloadInfo = await this.getDatasetDownloadURL(
      server,
      datasetID,
      versionNumber
    );

    let output = '';
    output += `Dataset: ${downloadInfo.dataset}\n`;
    output += `Version: ${downloadInfo.version}\n`;
    output += `Download URL: ${downloadInfo.url}\n`;
    output += `Status: Ready for download\n`;

    return output;
  }

  /**
   * Upload a dataset
   */
  async uploadDataset(
    server: string,
    datasetID: string,
    filePath: string,
    isNew: boolean
  ): Promise<void> {
    // Check if file exists
    const exists = await this.fs.exists(filePath);
    if (!exists) {
      throw new Error(`File does not exist: ${filePath}`);
    }

    if (isNew) {
      await this.createNewDataset(server, filePath);
    } else {
      await this.uploadToExistingDataset(server, datasetID, filePath);
    }
  }

  /**
   * Create a new dataset
   */
  private async createNewDataset(server: string, filePath: string): Promise<void> {
    const fileName = path.basename(filePath);
    console.log(`Creating new dataset from file: ${filePath}`);
    console.log(`Dataset name: ${fileName}`);
    console.log(`Server: ${server}`);

    const token = await this.authService.ensureValidToken();

    // Create form data
    const formData = new URLSearchParams();
    formData.append('name', fileName);

    const url = `https://${server}/user/datasets`;
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token.idToken}`,
        'Content-Type': 'application/x-www-form-urlencoded',
      },
      body: formData.toString(),
    });

    const body = await resp.text();
    console.log(`DEBUG: Create dataset response status: ${resp.status}`);
    console.log(`DEBUG: Create dataset response body: ${body}`);

    if (!resp.ok) {
      throw new Error(`Failed to create dataset (status ${resp.status}): ${body}`);
    }

    // Parse response to get dataset UUID
    const result = JSON.parse(body) as Record<string, any>;
    const datasetID = result.repo_id as string;

    if (!datasetID) {
      throw new Error('Could not find dataset ID in response');
    }

    console.log(`Created dataset with ID: ${datasetID}`);

    // Now upload the file to the new dataset
    console.log('Uploading file to new dataset...');
    await this.uploadToExistingDataset(server, datasetID, filePath);
  }

  /**
   * Upload to an existing dataset
   */
  private async uploadToExistingDataset(
    server: string,
    datasetID: string,
    filePath: string
  ): Promise<void> {
    console.log(`Uploading file to existing dataset: ${datasetID}`);
    console.log(`File: ${filePath}`);
    console.log(`Server: ${server}`);

    const token = await this.authService.ensureValidToken();

    // Step 1: Request presigned URL
    const presignedData = { _presigned: true };
    const url = `https://${server}/datasets/${datasetID}/versions`;

    const presignedResp = await fetch(url, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token.idToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(presignedData),
    });

    const presignedBody = await presignedResp.text();

    if (!presignedResp.ok) {
      throw new Error(
        `Failed to get presigned URL (status ${presignedResp.status}): ${presignedBody}`
      );
    }

    const presignedResponse = JSON.parse(presignedBody) as Record<string, any>;
    const presignedURL = presignedResponse.presigned_url as string;
    const uploadID = presignedResponse.upload_id as string;

    if (!presignedURL || !uploadID) {
      throw new Error('Presigned URL or upload ID not found in response');
    }

    // Step 2: Upload file to presigned URL
    const fileContent = await this.fs.readFile(filePath, 'binary' as BufferEncoding);
    const fileName = path.basename(filePath);

    // Create multipart form data manually (Node.js doesn't have FormData in older versions)
    const boundary = `----WebKitFormBoundary${Date.now()}`;
    const formDataParts: string[] = [];

    formDataParts.push(`--${boundary}\r\n`);
    formDataParts.push(`Content-Disposition: form-data; name="file"; filename="${fileName}"\r\n`);
    formDataParts.push(`Content-Type: application/octet-stream\r\n\r\n`);
    formDataParts.push(fileContent);
    formDataParts.push(`\r\n--${boundary}--\r\n`);

    const formDataBody = formDataParts.join('');

    const uploadResp = await fetch(presignedURL, {
      method: 'PUT',
      headers: {
        'Content-Type': `multipart/form-data; boundary=${boundary}`,
      },
      body: formDataBody,
    });

    const uploadBody = await uploadResp.text();

    if (!uploadResp.ok) {
      throw new Error(
        `Failed to upload file (status ${uploadResp.status}): ${uploadBody}`
      );
    }

    // Step 3: Close the upload
    const closeData = {
      action: 'close',
      upload_id: uploadID,
    };

    const closeResp = await fetch(url, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${token.idToken}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(closeData),
    });

    const closeBody = await closeResp.text();

    if (!closeResp.ok) {
      throw new Error(
        `Failed to close upload (status ${closeResp.status}): ${closeBody}`
      );
    }

    console.log(`Successfully uploaded file to dataset ${datasetID}`);
  }
}
