package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jyothri/hdd/db"
)

func TestDupFilter(t *testing.T) {
	for _, c := range []struct {
		query string
		want  db.DupFilter
		ok    bool
	}{
		{"", db.DupFilter{Kind: db.DupFile}, true},
		{"kind=folder&source=agent:3&across=1&min_size=1048576&hide_same_physical=1",
			db.DupFilter{Kind: db.DupFolder, Source: "agent:3", Across: true, MinSize: 1 << 20, HideSamePhysical: true}, true},
		{"kind=photo&across=0", db.DupFilter{Kind: db.DupPhoto}, true},
		{"kind=gmail", db.DupFilter{}, false},
		{"min_size=-1", db.DupFilter{}, false},
		{"min_size=big", db.DupFilter{}, false},
	} {
		got, ok := dupFilter(httptest.NewRequest("GET", "/api/duplicates/groups?"+c.query, nil))
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%q: %+v, %v; want %+v, %v", c.query, got, ok, c.want, c.ok)
		}
	}
}

func TestDupMembersNeedsAKindAndKey(t *testing.T) {
	for _, query := range []string{"", "kind=file", "key=md5:x:1", "kind=gmail&key=x"} {
		w := httptest.NewRecorder()
		DupMembersHandler(w, httptest.NewRequest("GET", "/api/duplicates/members?"+query, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", query, w.Code)
		}
	}
}
