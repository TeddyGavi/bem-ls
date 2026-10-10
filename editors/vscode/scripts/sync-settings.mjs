// Generates contributes.configuration.properties in package.json from the
// JSON schema of bem.config.json, so editor settings mirror the config file.
// Run with --check to fail instead of writing when they differ.
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const schemaPath = join(here, '..', '..', '..', 'schema', 'bem.config.schema.json');
const pkgPath = join(here, '..', 'package.json');

export function settingsFromSchema(schema) {
  const props = {};
  const severity = schema.definitions.severity;
  const resolve = (s) => (s.$ref === '#/definitions/severity' ? { ...severity, description: s.description } : s);
  // VS Code would send every default on every change; only keep defaults
  // that the server shares (enums and booleans), so the server's own
  // defaults apply everywhere else.
  const convert = (s) => {
    s = resolve(s);
    const out = {};
    if (s.type) out.type = s.type;
    if (s.enum) out.enum = s.enum;
    if (s.oneOf) out.type = [...new Set(s.oneOf.map((o) => o.type))];
    if (s.items) out.items = resolve(s.items);
    if (s.properties) {
      out.properties = Object.fromEntries(Object.entries(s.properties).map(([k, v]) => [k, convert(v)]));
      out.additionalProperties = false;
    }
    if (s.additionalProperties && typeof s.additionalProperties === 'object') out.additionalProperties = s.additionalProperties;
    if (s.minimum !== undefined) out.minimum = s.minimum;
    if (s.maximum !== undefined) out.maximum = s.maximum;
    let desc = s.description ?? '';
    if (s.default !== undefined) {
      if (s.type === 'boolean' || s.enum) out.default = s.default;
      else desc += `${desc ? ' ' : ''}Default: \`${JSON.stringify(s.default)}\`.`;
    }
    if (desc) out.markdownDescription = desc;
    return out;
  };
  for (const [group, def] of Object.entries(schema.properties)) {
    if (group === '$schema') continue;
    // Groups of independent options become one setting per option; maps
    // (rules) and lists stay single settings.
    if (def.type === 'object' && def.properties && group !== 'rules') {
      for (const [key, sub] of Object.entries(def.properties)) props[`bem.${group}.${key}`] = convert(sub);
    } else {
      props[`bem.${group}`] = convert(def);
    }
  }
  props['bem.server.path'] = {
    type: 'string',
    markdownDescription: 'Path to the `bem-ls` binary. Empty: the bundled binary, then `bem-ls` on the `PATH`.',
    scope: 'machine-overridable',
  };
  props['bem.trace.server'] = {
    type: 'string',
    enum: ['off', 'messages', 'verbose'],
    default: 'off',
    markdownDescription: 'Log the messages exchanged with the server to the BEM output channel.',
  };
  return props;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const schema = JSON.parse(readFileSync(schemaPath, 'utf8'));
  const pkg = JSON.parse(readFileSync(pkgPath, 'utf8'));
  const props = settingsFromSchema(schema);
  const current = JSON.stringify(pkg.contributes.configuration.properties);
  if (current === JSON.stringify(props)) {
    console.log('settings are up to date');
  } else if (process.argv.includes('--check')) {
    console.error('package.json settings are out of date: run npm run sync-settings');
    process.exit(1);
  } else {
    pkg.contributes.configuration.properties = props;
    writeFileSync(pkgPath, JSON.stringify(pkg, null, 2) + '\n');
    console.log(`wrote ${Object.keys(props).length} settings`);
  }
}
