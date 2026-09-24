package constants

import (
	"reflect"
	"testing"
)

func TestFrontendOrigins(t *testing.T) {
	original := FrontendUrl
	t.Cleanup(func() { FrontendUrl = original })

	cases := map[string][]string{
		"http://localhost:5173":                                {"http://localhost:5173"},
		"https://sm.example.com/, http://192.168.1.118:5173 ,": {"https://sm.example.com", "http://192.168.1.118:5173"},
		"": nil,
	}
	for value, want := range cases {
		FrontendUrl = value
		if got := FrontendOrigins(); !reflect.DeepEqual(got, want) {
			t.Errorf("FrontendOrigins() with %q = %q, want %q", value, got, want)
		}
	}
}
