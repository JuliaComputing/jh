/**
 * Dataset-related types
 * Migrated from datasets.go
 */

export interface Dataset {
  id: string;
  name: string;
  description: string;
  visibility: string;
  groups: string[];
  format: string | null;
  credentials_url: string;
  owner: Owner;
  storage: Storage;
  version: string;
  versions: Version[];
  size: number;
  downloadURL: string;
  tags: string[];
  license: License;
  type: string;
  lastModified: string; // ISO date string
}

export interface Owner {
  username: string;
  type: string;
}

export interface Storage {
  bucket_region: string;
  bucket: string;
  prefix: string;
  vendor: string;
}

export interface Version {
  project: string | null;
  uploader: Uploader;
  blobstore_path: string;
  date: string; // ISO date string
  size: number;
  version: number;
}

export interface Uploader {
  username: string;
}

export interface License {
  url: string | null;
  name: string;
  text: string;
  spdx_id: string;
}

export interface DatasetDownloadURL {
  dataset_id: string;
  version: string;
  dataset: string;
  url: string;
}
