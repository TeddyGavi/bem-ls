// Finding the bem-ls binary. Kept free of the vscode module so it can be
// unit-tested with node --test.
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';

export interface ResolveOptions {
  /** bem.server.path, if set. */
  configured?: string;
  /** The extension's install directory. */
  extensionPath: string;
  /** The first workspace folder, for ${workspaceFolder} in the setting. */
  workspaceFolder?: string;
  platform?: NodeJS.Platform;
  /** PATH value to search. */
  envPath?: string;
  exists?: (p: string) => boolean;
}

export interface Resolved {
  command: string;
  source: 'setting' | 'bundled' | 'path';
}

export function binaryName(platform: NodeJS.Platform = process.platform): string {
  return platform === 'win32' ? 'bem-ls.exe' : 'bem-ls';
}

/** Expands ~ and ${workspaceFolder} in a configured path. */
export function expandPath(p: string, workspaceFolder?: string): string {
  let out = p.trim();
  if (out === '~' || out.startsWith('~/') || out.startsWith('~\\')) {
    out = path.join(os.homedir(), out.slice(1));
  }
  if (workspaceFolder) {
    out = out.split('${workspaceFolder}').join(workspaceFolder);
  }
  return out;
}

/**
 * Picks the server binary: the bem.server.path setting, then the binary
 * bundled in the platform-specific VSIX (bin/bem-ls), then bem-ls on PATH.
 * Returns undefined if none exists. A configured path that doesn't exist is
 * an error, so a typo isn't silently replaced by another binary.
 */
export function resolveServer(opts: ResolveOptions): Resolved | undefined {
  const platform = opts.platform ?? process.platform;
  const exists = opts.exists ?? ((p: string) => fs.existsSync(p));
  const name = binaryName(platform);

  if (opts.configured && opts.configured.trim() !== '') {
    const p = expandPath(opts.configured, opts.workspaceFolder);
    if (!exists(p)) {
      throw new Error(`bem.server.path points to ${p}, which does not exist`);
    }
    return { command: p, source: 'setting' };
  }

  const bundled = path.join(opts.extensionPath, 'bin', name);
  if (exists(bundled)) {
    return { command: bundled, source: 'bundled' };
  }

  const sep = platform === 'win32' ? ';' : ':';
  for (const dir of (opts.envPath ?? process.env.PATH ?? '').split(sep)) {
    if (!dir) continue;
    const candidate = path.join(dir, name);
    if (exists(candidate)) {
      return { command: candidate, source: 'path' };
    }
  }
  return undefined;
}
