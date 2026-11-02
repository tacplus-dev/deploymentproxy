package deploymentproxy

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestNoFormerName proves the rename to deploymentproxy (#9) is complete: no file of the repository uses
// the former name, except HISTORY.md (append-only), ROADMAP.md (Jody's) and a line that says it was the
// former name ("formerly").
func TestNoFormerName(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	const former = "ns" + "proxy" // so that this file does not name it
	for _, path := range strings.Fields(string(out)) {
		if path == "HISTORY.md" || path == "ROADMAP.md" {
			continue
		}
		if strings.Contains(strings.ToLower(path), former) {
			t.Errorf("%s: the path names %s", path, former)
		}
		f, err := os.Open(path)
		if os.IsNotExist(err) {
			continue // deleted, not yet staged
		}
		if err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(f)
		s.Buffer(nil, 1<<20)
		for n := 1; s.Scan(); n++ {
			line := strings.ToLower(s.Text())
			if strings.Contains(line, former) && !strings.Contains(line, "formerly") {
				t.Errorf("%s:%d: %s", path, n, s.Text())
			}
		}
		if err := s.Err(); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		f.Close()
	}
}
