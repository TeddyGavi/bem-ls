// Packages platform-specific VSIX files, each with its own bem-ls binary.
//
//   node scripts/package.mjs <target|all> [version]
//
// target is a vsce target (darwin-arm64, linux-x64, win32-x64, ...). The Go
// binary is cross-compiled with CGO disabled, so every target can be built
// on any machine. version is stamped into the binary; the extension's own
// version comes from package.json (CI sets it from the tag first).
import { execFileSync } from 'node:child_process';
import { copyFileSync, mkdirSync, readFileSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const ext = join(dirname(fileURLToPath(import.meta.url)), '..');
const repo = join(ext, '..', '..');

export const targets = {
  'darwin-arm64': ['darwin', 'arm64'],
  'darwin-x64': ['darwin', 'amd64'],
  'linux-x64': ['linux', 'amd64'],
  'linux-arm64': ['linux', 'arm64'],
  'alpine-x64': ['linux', 'amd64'],
  'alpine-arm64': ['linux', 'arm64'],
  'win32-x64': ['windows', 'amd64'],
  'win32-arm64': ['windows', 'arm64'],
};

function run(cmd, args, opts = {}) {
  console.log(`$ ${cmd} ${args.join(' ')}`);
  execFileSync(cmd, args, { stdio: 'inherit', ...opts });
}

function packageTarget(target, version) {
  const [goos, goarch] = targets[target];
  const bin = join(ext, 'bin');
  rmSync(bin, { recursive: true, force: true }); // exactly one binary per VSIX
  mkdirSync(bin, { recursive: true });
  const exe = join(bin, goos === 'windows' ? 'bem-ls.exe' : 'bem-ls');
  run('go', ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version}`, '-o', exe, './cmd/bem-ls'], {
    cwd: repo,
    env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0' },
  });
  const pkg = JSON.parse(readFileSync(join(ext, 'package.json'), 'utf8'));
  const out = join(ext, 'vsix', `${pkg.name}-${target}-${pkg.version}.vsix`);
  mkdirSync(dirname(out), { recursive: true });
  run('npx', ['--no-install', 'vsce', 'package', '--target', target, '--out', out], { cwd: ext });
  return out;
}

const [arg, version = 'dev'] = process.argv.slice(2);
if (!arg || (arg !== 'all' && !targets[arg])) {
  console.error(`usage: node scripts/package.mjs <${Object.keys(targets).join('|')}|all> [version]`);
  process.exit(2);
}
run('npm', ['run', 'build'], { cwd: ext });
copyFileSync(join(repo, 'LICENSE'), join(ext, 'LICENSE'));
const built = (arg === 'all' ? Object.keys(targets) : [arg]).map((t) => packageTarget(t, version));
console.log(`\nbuilt:\n  ${built.join('\n  ')}`);
