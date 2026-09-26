package semver

import "testing"

func TestParse(t *testing.T) {
	good := map[string]Version{
		"0.1.0":    {0, 1, 0},
		"1.2.3":    {1, 2, 3},
		"10.20.30": {10, 20, 30},
	}
	for s, want := range good {
		got, err := Parse(s)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", s, got, err, want)
		}
		if got.String() != s {
			t.Errorf("String() = %q, want %q", got.String(), s)
		}
	}
	for _, s := range []string{"", "1", "1.2", "1.2.3.4", "v1.2.3", "1.2.3-rc1", "01.2.3", "1.-2.3", "1..3", "a.b.c", " 1.2.3"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q): want an error", s)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.1.1", -1},
		{"0.2.0", "0.1.9", 1},
		{"1.0.0", "0.99.99", 1},
		{"0.10.0", "0.9.0", 1},
	}
	for _, c := range cases {
		a, b := mustParse(t, c.a), mustParse(t, c.b)
		if got := a.Compare(b); got != c.want {
			t.Errorf("%s vs %s = %d, want %d", c.a, c.b, got, c.want)
		}
		if a.Less(b) != (c.want < 0) {
			t.Errorf("%s.Less(%s) wrong", c.a, c.b)
		}
	}
}

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
