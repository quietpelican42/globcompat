#!/usr/bin/env node
// Differential oracle used by breakdown_test.go (build tag "differential"):
// reads a JSON array of {pattern, path, opts} from argv[2], computes
// picomatch.isMatch for each with the given options, and writes a JSON
// array of {ok, match} to stdout. See oracle.cjs for the plain
// (default-options) version used by differential_test.go.
'use strict';

const fs = require('fs');
const picomatch = require('picomatch');

const infile = process.argv[2];
const cases = JSON.parse(fs.readFileSync(infile, 'utf8'));

const out = new Array(cases.length);
for (let i = 0; i < cases.length; i++) {
  const c = cases[i];
  try {
    out[i] = { ok: true, match: picomatch.isMatch(c.path, c.pattern, c.opts || {}) };
  } catch (e) {
    out[i] = { ok: false, error: String((e && e.message) || e) };
  }
}
process.stdout.write(JSON.stringify(out));
