#!/usr/bin/env node
// Differential oracle used by differential_test.go (build tag
// "differential"): reads a JSON array of {pattern, path} from argv[2],
// computes picomatch.isMatch for each with default options, and writes a
// JSON array of {ok, match} to stdout. Resolves the "picomatch" module via
// NODE_PATH, which differential_test.go points at its own npm-installed
// copy under testdata/.upstream-cache (gitignored, fetched at test time).
'use strict';

const fs = require('fs');
const picomatch = require('picomatch');

const infile = process.argv[2];
const cases = JSON.parse(fs.readFileSync(infile, 'utf8'));

const out = new Array(cases.length);
for (let i = 0; i < cases.length; i++) {
  const c = cases[i];
  try {
    out[i] = { ok: true, match: picomatch.isMatch(c.path, c.pattern) };
  } catch (e) {
    out[i] = { ok: false, error: String((e && e.message) || e) };
  }
}
process.stdout.write(JSON.stringify(out));
