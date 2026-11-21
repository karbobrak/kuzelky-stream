package google

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeYouTube imitates the token endpoint and the parts of the Data API the app uses.
func fakeYouTube(t *testing.T) (*httptest.Server, *[]string) {
	var calls []string
	var tokens atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			_ = r.ParseForm()
			if r.Form.Get("refresh_token") != "R" {
				w.WriteHeader(400)
				io.WriteString(w, `{"error":"invalid_grant"}`)
				return
			}
			tokens.Add(1)
			io.WriteString(w, `{"access_token":"A","expires_in":3600}`)
			return
		}
		if r.Header.Get("Authorization") != "Bearer A" {
			w.WriteHeader(401)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /liveStreams":
			io.WriteString(w, `{"items":[]}`)
		case "POST /liveStreams":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["contentDetails"].(map[string]any)["isReusable"] != true {
				t.Error("stream must be reusable")
			}
			io.WriteString(w, `{"id":"S1","cdn":{"ingestionInfo":{"streamName":"key-1","ingestionAddress":"rtmp://a.rtmp.youtube.com/live2"}}}`)
		case "POST /liveBroadcasts":
			var body struct {
				ContentDetails map[string]any `json:"contentDetails"`
				Status         map[string]any `json:"status"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.ContentDetails["enableAutoStop"] != false || body.ContentDetails["enableAutoStart"] != true {
				t.Errorf("broadcast must auto-start and not auto-stop: %v", body.ContentDetails)
			}
			if body.Status["privacyStatus"] != "unlisted" {
				t.Errorf("privacy = %v", body.Status["privacyStatus"])
			}
			io.WriteString(w, `{"id":"B1","snippet":{"title":"Test"},"status":{"lifeCycleStatus":"created","privacyStatus":"unlisted"}}`)
		case "POST /liveBroadcasts/bind":
			if r.URL.Query().Get("id") != "B1" || r.URL.Query().Get("streamId") != "S1" {
				t.Errorf("bind query = %s", r.URL.RawQuery)
			}
			io.WriteString(w, `{"id":"B1"}`)
		case "GET /liveBroadcasts":
			io.WriteString(w, `{"items":[{"id":"B1","snippet":{"title":"Test"},"status":{"lifeCycleStatus":"ready","privacyStatus":"unlisted"},
				"contentDetails":{"enableAutoStart":true,"monitorStream":{"enableMonitorStream":true}}}]}`)
		case "POST /liveBroadcasts/transition":
			if r.URL.Query().Get("broadcastStatus") == "live" {
				w.WriteHeader(403)
				io.WriteString(w, `{"error":{"message":"Invalid transition","errors":[{"reason":"invalidTransition"}]}}`)
				return
			}
			io.WriteString(w, `{"id":"B1"}`)
		default:
			w.WriteHeader(404)
			io.WriteString(w, `{"error":{"message":"not found","errors":[{"reason":"notFound"}]}}`)
		}
	}))
	tokenEndpoint = srv.URL + "/token"
	apiBase = srv.URL + "/"
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestBroadcastFlow(t *testing.T) {
	_, calls := fakeYouTube(t)
	c := &Client{ClientID: "id", ClientSecret: "secret", RefreshToken: "R"}
	ctx := t.Context()

	st, err := c.EnsureStream(ctx, "")
	if err != nil || st.ID != "S1" || st.Key != "key-1" || !strings.HasPrefix(st.Address, "rtmp://") {
		t.Fatalf("EnsureStream = %+v, %v", st, err)
	}
	b, err := c.CreateBroadcast(ctx, "Test", "unlisted")
	if err != nil || b.ID != "B1" {
		t.Fatalf("CreateBroadcast = %+v, %v", b, err)
	}
	if err := c.Bind(ctx, b.ID, st.ID); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetBroadcast(ctx, "B1")
	if err != nil || got == nil || got.LifeCycle != "ready" || got.Ended() || !got.MonitorStream || !got.AutoStart {
		t.Fatalf("GetBroadcast = %+v, %v", got, err)
	}
	if err := c.Transition(ctx, "B1", "live"); !IsInvalidTransition(err) {
		t.Fatalf("transition to live from ready: err = %v, want invalidTransition", err)
	}
	if err := c.Transition(ctx, "B1", "testing"); err != nil {
		t.Fatalf("transition to testing: %v", err)
	}
	if b.WatchURL() != "https://www.youtube.com/watch?v=B1" {
		t.Fatal(b.WatchURL())
	}
	// One token refresh for all calls.
	n := 0
	for _, c := range *calls {
		if c == "POST /token" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("token refreshed %d times, want 1 (%v)", n, *calls)
	}
}

func TestRevokedLogin(t *testing.T) {
	fakeYouTube(t)
	c := &Client{ClientID: "id", ClientSecret: "secret", RefreshToken: "revoked"}
	if _, err := c.MyChannel(t.Context()); err != ErrAuth {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}

func TestAuthURL(t *testing.T) {
	l := NewLogin("http://127.0.0.1:8080/oauth2/callback", ScopeYouTube)
	u := l.AuthURL("cid")
	for _, want := range []string{"code_challenge_method=S256", "access_type=offline", "client_id=cid",
		"redirect_uri=http%3A%2F%2F127.0.0.1%3A8080%2Foauth2%2Fcallback", "state=" + l.State} {
		if !strings.Contains(u, want) {
			t.Errorf("auth URL missing %s: %s", want, u)
		}
	}
}
