import * as fs from 'fs/promises';
import * as fsSync from 'fs';
import * as os from 'os';
import * as path from 'path';
import {
  IFileSystem,
  WriteFileOptions,
  MkdirOptions,
  FileStats,
  FileHandle,
} from '../types/filesystem';

/**
 * Node.js implementation of the filesystem interface
 */
export class NodeFileSystem implements IFileSystem {
  async readFile(filePath: string, encoding: BufferEncoding): Promise<string> {
    return fs.readFile(filePath, encoding);
  }

  async writeFile(
    filePath: string,
    content: string,
    options?: WriteFileOptions
  ): Promise<void> {
    return fs.writeFile(filePath, content, options);
  }

  async exists(filePath: string): Promise<boolean> {
    try {
      await fs.access(filePath);
      return true;
    } catch {
      return false;
    }
  }

  async stat(filePath: string): Promise<FileStats> {
    const stats = await fs.stat(filePath);
    return {
      isFile: () => stats.isFile(),
      isDirectory: () => stats.isDirectory(),
      size: stats.size,
      mtime: stats.mtime,
    };
  }

  async mkdir(dirPath: string, options?: MkdirOptions): Promise<void> {
    await fs.mkdir(dirPath, options);
  }

  async rename(oldPath: string, newPath: string): Promise<void> {
    await fs.rename(oldPath, newPath);
  }

  async unlink(filePath: string): Promise<void> {
    await fs.unlink(filePath);
  }

  async chmod(filePath: string, mode: number): Promise<void> {
    await fs.chmod(filePath, mode);
  }

  homedir(): string {
    return os.homedir();
  }

  async mkdtemp(prefix: string): Promise<string> {
    // mkdtemp creates a directory with the prefix + random suffix
    // If prefix contains a directory, it will be created there
    return fs.mkdtemp(prefix);
  }

  async open(filePath: string, flags: string, mode?: number): Promise<FileHandle> {
    const handle = await fs.open(filePath, flags, mode);
    return {
      write: async (data: string) => {
        await handle.write(data);
      },
      sync: async () => {
        await handle.sync();
      },
      close: async () => {
        await handle.close();
      },
    };
  }
}

/**
 * Default filesystem instance for Node.js
 */
export const defaultFileSystem = new NodeFileSystem();
