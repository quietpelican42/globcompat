#!/usr/bin/env node
// Differential oracle used by differential_test.go (build tag
// "differential"): reads a JSON array of {pattern, path} from argv[2],
// computes picomatch.isMatch for each with default options, and writes a
// JSON array of {ok, match} to stdout. Resolves "picomatch"/"braces" via
// NODE_PATH, which differential_test.go points at its own npm-installed
// copy under testdata/.upstream-cache (gitignored, fetched at test time).
//
// Plain picomatch.isMatch (and micromatch.isMatch, which delegates to it)
// does not expand numeric/alpha brace ranges like "{2..11}" on its own —
// that needs an expandRange callback, which is exactly what real callers
// get for free from a full brace-aware pipeline (fast-glob, lint-staged).
// expandRangeFn below wires picomatch's expandRange hook to the real
// "braces" package, so this oracle measures what those tools' users
// actually see, not picomatch's un-expanded fallback (a raw, sorted
// character class over the endpoint strings, e.g. "v{2..11}" degenerates
// to "v[11-2]").
'use strict';

const fs = require('fs');
const picomatch = require('picomatch');
const braces = require('braces');

function expandRangeFn(...allArgs) {
  const parts = allArgs.slice(0, -1); // drop the trailing options object
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
    out[i] = { ok: true, match: picomatch.isMatch(c.path, c.pattern, { expandRange: expandRangeFn }) };
  } catch (e) {
    out[i] = { ok: false, error: String((e && e.message) || e) };
  }
}
process.stdout.write(JSON.stringify(out));
