package google

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Channel is the signed-in channel.
type Channel struct {
	ID, Title string
}

func (c *Client) MyChannel(ctx context.Context) (Channel, error) {
	var r struct {
		Items []struct {
			ID      string `json:"id"`
			Snippet struct {
				Title string `json:"title"`
			} `json:"snippet"`
		} `json:"items"`
	}
	if err := c.call(ctx, "GET", "channels", url.Values{"part": {"snippet"}, "mine": {"true"}}, nil, &r); err != nil {
		return Channel{}, err
	}
	if len(r.Items) == 0 {
		return Channel{}, fmt.Errorf("účet nemá YouTube kanál")
	}
	return Channel{ID: r.Items[0].ID, Title: r.Items[0].Snippet.Title}, nil
}

// ---------------------------------------------------------------- live stream (ingest point + key)

// Stream is the reusable ingest point the app sends video to.
type Stream struct {
	ID      string
	Address string // rtmp://a.rtmp.youtube.com/live2
	Key     string
}

type liveStream struct {
	ID  string `json:"id"`
	CDN struct {
		IngestionInfo struct {
			StreamName       string `json:"streamName"`
			IngestionAddress string `json:"ingestionAddress"`
		} `json:"ingestionInfo"`
	} `json:"cdn"`
	Status struct {
		StreamStatus string `json:"streamStatus"` // active | created | error | inactive | ready
		HealthStatus struct {
			Status              string `json:"status"` // good | ok | bad | noData
			ConfigurationIssues []struct {
				Severity    string `json:"severity"`
				Reason      string `json:"reason"`
				Description string `json:"description"`
			} `json:"configurationIssues"`
		} `json:"healthStatus"`
	} `json:"status"`
}

func (s liveStream) toStream() Stream {
	return Stream{ID: s.ID, Address: s.CDN.IngestionInfo.IngestionAddress, Key: s.CDN.IngestionInfo.StreamName}
}

func (c *Client) getStream(ctx context.Context, id string) (*liveStream, error) {
	var r struct {
		Items []liveStream `json:"items"`
	}
	err := c.call(ctx, "GET", "liveStreams", url.Values{"part": {"id,cdn,status"}, "id": {id}}, nil, &r)
	if err != nil || len(r.Items) == 0 {
		return nil, err
	}
	return &r.Items[0], nil
}

// EnsureStream returns the stream with id, or creates a new reusable one when id is empty or gone.
func (c *Client) EnsureStream(ctx context.Context, id string) (Stream, error) {
	if id != "" {
		s, err := c.getStream(ctx, id)
		if err != nil && !isNotFound(err) {
			return Stream{}, err
		}
		if s != nil {
			return s.toStream(), nil
		}
	}
	body := map[string]any{
		"snippet":        map[string]any{"title": "KuzelkyStream"},
		"cdn":            map[string]any{"ingestionType": "rtmp", "resolution": "variable", "frameRate": "variable"},
		"contentDetails": map[string]any{"isReusable": true},
	}
	var s liveStream
	if err := c.call(ctx, "POST", "liveStreams", url.Values{"part": {"snippet,cdn,contentDetails,status"}}, body, &s); err != nil {
		return Stream{}, err
	}
	return s.toStream(), nil
}

// StreamHealth is what YouTube thinks of the incoming video.
type StreamHealth struct {
	Status string   // active / inactive / …
	Health string   // good / ok / bad / noData
	Issues []string // configuration problems reported by YouTube
}

func (c *Client) StreamHealth(ctx context.Context, id string) (StreamHealth, error) {
	s, err := c.getStream(ctx, id)
	if err != nil || s == nil {
		return StreamHealth{}, err
	}
	h := StreamHealth{Status: s.Status.StreamStatus, Health: s.Status.HealthStatus.Status}
	for _, i := range s.Status.HealthStatus.ConfigurationIssues {
		h.Issues = append(h.Issues, strings.TrimSpace(i.Severity+": "+i.Description))
	}
	return h, nil
}

// ---------------------------------------------------------------- broadcast (the video viewers watch)

type Broadcast struct {
	ID        string
	Title     string
	Privacy   string
	LifeCycle string // created | ready | testing | live | complete | revoked | …
	// MonitorStream: YouTube requires ready → testing → live when the monitor stream is on.
	MonitorStream bool
	AutoStart     bool
}

func (b Broadcast) WatchURL() string { return "https://www.youtube.com/watch?v=" + b.ID }

// Ended reports whether the broadcast can no longer go live.
func (b Broadcast) Ended() bool { return b.LifeCycle == "complete" || b.LifeCycle == "revoked" }

type liveBroadcast struct {
	ID      string `json:"id"`
	Snippet struct {
		Title string `json:"title"`
	} `json:"snippet"`
	Status struct {
		LifeCycleStatus string `json:"lifeCycleStatus"`
		PrivacyStatus   string `json:"privacyStatus"`
	} `json:"status"`
	ContentDetails struct {
		EnableAutoStart bool `json:"enableAutoStart"`
		MonitorStream   struct {
			EnableMonitorStream bool `json:"enableMonitorStream"`
		} `json:"monitorStream"`
	} `json:"contentDetails"`
}

func (b liveBroadcast) toBroadcast() Broadcast {
	return Broadcast{ID: b.ID, Title: b.Snippet.Title, Privacy: b.Status.PrivacyStatus, LifeCycle: b.Status.LifeCycleStatus,
		MonitorStream: b.ContentDetails.MonitorStream.EnableMonitorStream, AutoStart: b.ContentDetails.EnableAutoStart}
}

// IsInvalidTransition reports the API's "invalidTransition" error.
func IsInvalidTransition(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Reason == "invalidTransition"
}

// GetBroadcast returns nil (no error) when the broadcast does not exist any more (deleted in Studio).
func (c *Client) GetBroadcast(ctx context.Context, id string) (*Broadcast, error) {
	var r struct {
		Items []liveBroadcast `json:"items"`
	}
	if err := c.call(ctx, "GET", "liveBroadcasts", url.Values{"part": {"id,snippet,status,contentDetails"}, "id": {id}}, nil, &r); err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(r.Items) == 0 {
		return nil, nil
	}
	b := r.Items[0].toBroadcast()
	return &b, nil
}

// CreateBroadcast creates a broadcast that starts by itself when video arrives and does NOT end
// by itself when it stops (so reconnects stay in the same video, at the same address).
func (c *Client) CreateBroadcast(ctx context.Context, title, privacy string) (Broadcast, error) {
	body := map[string]any{
		"snippet": map[string]any{
			"title":              title,
			"scheduledStartTime": time.Now().UTC().Format(time.RFC3339),
		},
		"status": map[string]any{
			"privacyStatus":           privacy,
			"selfDeclaredMadeForKids": false,
		},
		"contentDetails": map[string]any{
			"enableAutoStart":   true,
			"enableAutoStop":    false,
			"enableDvr":         true,
			"latencyPreference": "normal",
			// Both fields are needed; with only enableMonitorStream YouTube may keep the monitor on.
			"monitorStream": map[string]any{"enableMonitorStream": false, "broadcastStreamDelayMs": 0},
		},
	}
	var b liveBroadcast
	err := c.call(ctx, "POST", "liveBroadcasts", url.Values{"part": {"snippet,status,contentDetails"}}, body, &b)
	return b.toBroadcast(), err
}

func (c *Client) Bind(ctx context.Context, broadcastID, streamID string) error {
	return c.call(ctx, "POST", "liveBroadcasts/bind",
		url.Values{"id": {broadcastID}, "streamId": {streamID}, "part": {"id"}}, nil, nil)
}

// Transition moves the broadcast to "live" or "complete".
func (c *Client) Transition(ctx context.Context, broadcastID, status string) error {
	return c.call(ctx, "POST", "liveBroadcasts/transition",
		url.Values{"id": {broadcastID}, "broadcastStatus": {status}, "part": {"id,status"}}, nil, nil)
}
