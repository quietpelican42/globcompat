//go:build differential

// Run with: go test -tags differential -run TestDifferential -v ./...
//
// This test is excluded from the default build/test (it needs network and
// Node) so `go build ./...` / `go test ./...` stay hermetic per the repo's
// definition of done. It fetches micromatch's own test fixtures (MIT
// licensed) directly from GitHub, pinned to a commit SHA and verified by
// sha256, into a gitignored cache directory, then differentials this
// package's Match against the real picomatch package (run through a small
// Node helper script) over the cross product of those fixtures plus this
// package's own random corpus generator.
package globcompat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const (
	upstreamCommit  = "8bd704ec0d9894693d35da425d827819916be920" // micromatch/micromatch tag 4.0.8
	patternsURL     = "https://raw.githubusercontent.com/micromatch/micromatch/" + upstreamCommit + "/test/_patterns.js"
	patternsSHA256  = "044ff405eab3c0f08b3d1ebfcb8a8db67980fdf1ac2ae0c3fba75d197840e9f9"
	fixturesURL     = "https://raw.githubusercontent.com/micromatch/micromatch/" + upstreamCommit + "/test/_fixtures.js"
	fixturesSHA256  = "dc663ccb413a4a59d570c0a2702982ccf1d4901cb19c45c695bbc77250f33a41"
	minAgreementPct = 99.5
)

func TestDifferential(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("differential test needs Node on PATH to run the real picomatch package; skipping")
	}

	cacheDir := filepath.Join("testdata", ".upstream-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}

	patternsFile, err := fetchPinned(cacheDir, "patterns.js", patternsURL, patternsSHA256)
	if err != nil {
		t.Skipf("could not fetch upstream test fixtures (no network?): %v", err)
	}
	fixturesFile, err := fetchPinned(cacheDir, "fixtures.js", fixturesURL, fixturesSHA256)
	if err != nil {
		t.Skipf("could not fetch upstream test fixtures (no network?): %v", err)
	}

	if !hasNodeModules(cacheDir, "picomatch", "braces") {
		cmd := exec.Command("npm", "install", "--no-save", "--no-audit", "--no-fund", "picomatch@4", "braces@3")
		cmd.Dir = cacheDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("could not npm install picomatch/braces (no network?): %v\n%s", err, out)
		}
	}

	patterns := extractJSStringArray(readFile(t, patternsFile))
	fixtures := extractJSStringArray(readFile(t, fixturesFile))
	if len(patterns) == 0 || len(fixtures) == 0 {
		t.Fatalf("parsed 0 patterns/fixtures from upstream files; format may have changed")
	}

	type oc struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	var cases []oc
	for _, p := range patterns {
		for _, f := range fixtures {
			cases = append(cases, oc{p, f})
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 5000; i++ {
		cases = append(cases, oc{patterns[r.Intn(len(patterns))], fixtures[r.Intn(len(fixtures))]})
	}

	inFile := filepath.Join(t.TempDir(), "cases.json")
	writeJSON(t, inFile, cases)

	oraclePath := filepath.Join("testdata", "oracle.cjs")
	start := time.Now()
	cmd := exec.Command(node, oraclePath, inFile)
	cmd.Dir = "." // module resolution walks up from here to testdata/.upstream-cache? handled via NODE_PATH below
	cmd.Env = append(os.Environ(), "NODE_PATH="+filepath.Join(cacheDir, "node_modules"))
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("node oracle failed: %v\n%s", err, ee.Stderr)
		}
		t.Fatalf("node oracle failed: %v", err)
	}
	t.Logf("oracle run over %d cases took %s", len(cases), time.Since(start))

	var results []struct {
		Ok    bool `json:"ok"`
		Match bool `json:"match"`
	}
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("parsing oracle output: %v", err)
	}

	total, agree := 0, 0
	var examples []string
	for i, c := range cases {
		if !results[i].Ok {
			continue
		}
		total++
		got, err := Match(c.Pattern, c.Path, Options{})
		if err == nil && got == results[i].Match {
			agree++
		} else if len(examples) < 20 {
			examples = append(examples, fmt.Sprintf("pattern=%q path=%q want=%v got=%v err=%v", c.Pattern, c.Path, results[i].Match, got, err))
		}
	}
	pct := 100 * float64(agree) / float64(total)
	t.Logf("agreement with picomatch: %d/%d (%.2f%%)", agree, total, pct)
	if pct < minAgreementPct {
		t.Errorf("agreement %.2f%% fell below the %.2f%% floor; examples:\n%s", pct, minAgreementPct, strings.Join(examples, "\n"))
	}
}

// hasNodeModules reports whether every named package already exists under
// dir/node_modules, so the npm install step can be skipped on a warm cache.
func hasNodeModules(dir string, pkgs ...string) bool {
	for _, p := range pkgs {
		if _, err := os.Stat(filepath.Join(dir, "node_modules", p)); err != nil {
			return false
		}
	}
	return true
}

func fetchPinned(dir, name, url, wantSHA256 string) (string, error) {
	dest := filepath.Join(dir, name)
	if data, err := os.ReadFile(dest); err == nil {
		if sha := sha256.Sum256(data); hex.EncodeToString(sha[:]) == wantSHA256 {
			return dest, nil
		}
	}
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	sha := sha256.Sum256(data)
	if got := hex.EncodeToString(sha[:]); got != wantSHA256 {
		return "", fmt.Errorf("sha256 mismatch for %s: got %s want %s", url, got, wantSHA256)
	}
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

var jsStringRe = regexp.MustCompile(`'((?:[^'\\]|\\.)*)'`)

func extractJSStringArray(data string) []string {
	matches := jsStringRe.FindAllStringSubmatch(data, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, strings.ReplaceAll(m[1], `\'`, `'`))
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating %s: %v", path, err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(v); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
