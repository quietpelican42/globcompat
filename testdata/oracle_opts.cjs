#!/usr/bin/env node
// Differential oracle used by breakdown_test.go (build tag "differential"):
// reads a JSON array of {pattern, path, opts} from argv[2], computes
// picomatch.isMatch for each with the given options, and writes a JSON
// array of {ok, match} to stdout. See oracle.cjs for the plain
// (default-options) version used by differential_test.go, and for why
// expandRange is wired to the real "braces" package.
'use strict';

const fs = require('fs');
const picomatch = require('picomatch');
const braces = require('braces');

function expandRangeFn(...allArgs) {
  const parts = allArgs.slice(0, -1);
  const rangeStr = parts.length >= 3
    ? `{${parts[0]}..${parts[1]}..${parts[2]}}`
    : `{${parts[0]}..${parts[1]}}`;
  const expanded = braces(rangeStr, { expand: true });
  return '(?:' + expanded.map(v => v.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|') + ')';
}

const infile = process.argv[2];
const cases = JSON.parse(fs.readFileSync(infile, 'utf8'));

const out = new Array(cases.length);
for (let i = 0; i < cases.length; i++) {
  const c = cases[i];
  try {
    const opts = Object.assign({}, c.opts || {}, { expandRange: expandRangeFn });
    out[i] = { ok: true, match: picomatch.isMatch(c.path, c.pattern, opts) };
  } catch (e) {
    out[i] = { ok: false, error: String((e && e.message) || e) };
  }
}
process.stdout.write(JSON.stringify(out));
