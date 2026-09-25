package twitter

import (
	"context"
	"encoding/json"
	"github.com/hecker-01/go-gallery/internal/extractor"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserIDShapes(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`{"data":{"user":{"result":{"rest_id":"42"}}}}`, "42"},
		{`{"data":{"user":{"result":{"legacy":{"id_str":"43"}}}}}`, "43"},
		{`{"data":{"user":{"result":{"legacy":false}}}}`, ""},
		{`{"data":{"user":{"result":[]}}}`, ""},
		{`{"data":{}}`, ""},
	} {
		var resp map[string]any
		json.Unmarshal([]byte(tc.raw), &resp)
		id, err := parseUserID(resp)
		if id != tc.want || (err == nil) != (tc.want != "") {
			t.Fatalf("%s: %q %v", tc.raw, id, err)
		}
	}
}
func TestUserExtractorReportsStartupAndPageErrors(t *testing.T) {
	for _, startup := range []bool{true, false} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !startup && contains(r.URL.Path, "UserByScreenName") {
				w.Write([]byte(`{"data":{"user":{"result":{"rest_id":"42"}}}}`))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}))
		e := &TwitterUserExtractor{base: newBase("https://x.com/test", extractor.ClientParams{HTTP: srv.Client(), Twitter: extractor.TwitterOptions{GuestToken: "fixture"}}), screenName: "test"}
		patchGraphQLBase(e, srv.URL)
		failures := 0
		for i := range e.Items(context.Background()) {
			if i.Kind == extractor.KindError && i.Err != nil {
				failures++
			}
		}
		srv.Close()
		if failures != 1 {
			t.Fatalf("startup=%v failures=%d", startup, failures)
		}
	}
}
