#!/usr/bin/env node
import { fileURLToPath } from 'url';
import { dirname, join } from 'path';
import { spawnSync } from 'child_process';

const __dirname = dirname(fileURLToPath(import.meta.url));
const adminDir = join(__dirname, '..', 'release', 'admin');
const releaseParser = join(__dirname, '..', 'release', 'scripts', 'smoke-admin-import-graph.mjs');

const result = spawnSync(process.execPath, [releaseParser, adminDir], { stdio: 'inherit' });
process.exit(result.status);
