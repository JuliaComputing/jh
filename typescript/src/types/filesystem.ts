/**
 * Filesystem abstraction interface to support both Node.js and VSCode APIs
 * This allows the CLI to work in both environments without modifications
 */
export interface IFileSystem {
  /**
   * Read a file as a string
   */
  readFile(path: string, encoding: BufferEncoding): Promise<string>;

  /**
   * Write a file with string content
   */
  writeFile(path: string, content: string, options?: WriteFileOptions): Promise<void>;

  /**
   * Check if a file or directory exists
   */
  exists(path: string): Promise<boolean>;

  /**
   * Get file stats
   */
  stat(path: string): Promise<FileStats>;

  /**
   * Create a directory recursively
   */
  mkdir(path: string, options?: MkdirOptions): Promise<void>;

  /**
   * Rename a file or directory
   */
  rename(oldPath: string, newPath: string): Promise<void>;

  /**
   * Remove a file
   */
  unlink(path: string): Promise<void>;

  /**
   * Change file permissions
   */
  chmod(path: string, mode: number): Promise<void>;

  /**
   * Get the user's home directory
   */
  homedir(): string;

  /**
   * Create a temporary file
   */
  mkdtemp(prefix: string): Promise<string>;

  /**
   * Open a file for writing
   */
  open(path: string, flags: string, mode?: number): Promise<FileHandle>;
}

export interface WriteFileOptions {
  encoding?: BufferEncoding;
  mode?: number;
  flag?: string;
}

export interface MkdirOptions {
  recursive?: boolean;
  mode?: number;
}

export interface FileStats {
  isFile(): boolean;
  isDirectory(): boolean;
  size: number;
  mtime: Date;
}

export interface FileHandle {
  write(data: string): Promise<void>;
  sync(): Promise<void>;
  close(): Promise<void>;
}
