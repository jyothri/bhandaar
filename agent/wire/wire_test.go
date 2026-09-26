package wire

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// wire must stay standard-library only: a require line would pull a whole
// module into driveagent's module graph.
func TestGoModHasNoRequire(t *testing.T) {
	f, err := os.Open("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.HasPrefix(strings.TrimSpace(sc.Text()), "require") {
			t.Fatalf("wire/go.mod has a require line: %q; wire must use the standard library only", sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}
