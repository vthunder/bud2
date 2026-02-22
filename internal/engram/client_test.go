package engram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newTestServer creates a test server that responds to one path with the given
// status code and body. Returns the client and a cleanup function.
func newTestServer(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, "test-key")
}

func TestHealth_OK(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.Health(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHealth_Error(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := c.Health(); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestIngestEpisode(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/episodes" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong auth header: %q", r.Header.Get("Authorization"))
		}
		var req IngestEpisodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if req.Content != "hello" {
			t.Errorf("expected content 'hello', got %q", req.Content)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(IngestResult{ID: "ep-abc", ShortID: "abc"})
	})

	result, err := c.IngestEpisode(IngestEpisodeRequest{Content: "hello", Source: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "ep-abc" {
		t.Errorf("expected ID 'ep-abc', got %q", result.ID)
	}
}

func TestIngestThought(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/thoughts" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["content"] != "a thought" {
			t.Errorf("expected content 'a thought', got %q", body["content"])
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(IngestResult{ID: "ep-thought"})
	})

	result, err := c.IngestThought("a thought")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ID != "ep-thought" {
		t.Errorf("expected ID 'ep-thought', got %q", result.ID)
	}
}

func TestSearch(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/engrams" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("query") != "test query" {
			t.Errorf("expected query param 'test query', got %q", r.URL.Query().Get("query"))
		}
		if r.URL.Query().Get("detail") != "full" {
			t.Errorf("expected detail=full")
		}
		traces := []*Trace{{ID: "tr-1", Summary: "a trace"}}
		json.NewEncoder(w).Encode(traces)
	})

	result, err := c.Search("test query", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Traces) != 1 || result.Traces[0].ID != "tr-1" {
		t.Errorf("unexpected traces: %+v", result.Traces)
	}
}

func TestListTraces(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		traces := []*Trace{
			{ID: "tr-1", Summary: "first"},
			{ID: "tr-2", Summary: "second"},
		}
		json.NewEncoder(w).Encode(traces)
	})

	traces, err := c.ListTraces()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(traces) != 2 {
		t.Errorf("expected 2 traces, got %d", len(traces))
	}
}

func TestGetTrace(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/engrams/tr-abc" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("level") != "1" {
			t.Errorf("expected level=1, got %q", r.URL.Query().Get("level"))
		}
		json.NewEncoder(w).Encode(Trace{ID: "tr-abc", Summary: "summary at L1"})
	})

	trace, err := c.GetTrace("tr-abc", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trace.ID != "tr-abc" {
		t.Errorf("expected ID 'tr-abc', got %q", trace.ID)
	}
}

func TestGetTraceContext(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/engrams/tr-xyz/context" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		ctx := TraceContext{
			Trace:   &Trace{ID: "tr-xyz"},
			Sources: []Episode{{ID: "ep-1"}},
		}
		json.NewEncoder(w).Encode(ctx)
	})

	ctx, err := c.GetTraceContext("tr-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx.Trace.ID != "tr-xyz" {
		t.Errorf("expected trace ID 'tr-xyz', got %q", ctx.Trace.ID)
	}
	if len(ctx.Sources) != 1 {
		t.Errorf("expected 1 source, got %d", len(ctx.Sources))
	}
}

func TestDeleteTrace(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/v1/engrams/tr-del" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.DeleteTrace("tr-del"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteTrace_Error(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(apiError{Error: "not found", Code: "NOT_FOUND"})
	})

	err := c.DeleteTrace("tr-missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestBoostTraces(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/engrams/boost" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		ids, ok := body["engram_ids"].([]any)
		if !ok || len(ids) != 2 {
			t.Errorf("expected 2 engram_ids, got %+v", body["engram_ids"])
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.BoostTraces([]string{"tr-1", "tr-2"}, 0.2, 0.1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetEpisode(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/episodes/ep-123" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(Episode{ID: "ep-123", Content: "the content"})
	})

	ep, err := c.GetEpisode("ep-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ep.Content != "the content" {
		t.Errorf("expected content 'the content', got %q", ep.Content)
	}
}

func TestGetRecentEpisodes(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("channel") != "ch-1" {
			t.Errorf("expected channel ch-1, got %q", r.URL.Query().Get("channel"))
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Errorf("expected limit 10, got %q", r.URL.Query().Get("limit"))
		}
		episodes := []*Episode{{ID: "ep-1"}, {ID: "ep-2"}}
		json.NewEncoder(w).Encode(episodes)
	})

	eps, err := c.GetRecentEpisodes("ch-1", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(eps) != 2 {
		t.Errorf("expected 2 episodes, got %d", len(eps))
	}
}

func TestGetUnconsolidatedEpisodeIDs(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("unconsolidated") != "true" {
			t.Errorf("expected unconsolidated=true")
		}
		resp := struct {
			IDs []string `json:"ids"`
		}{IDs: []string{"ep-1", "ep-2", "ep-3"}}
		json.NewEncoder(w).Encode(resp)
	})

	ids, err := c.GetUnconsolidatedEpisodeIDs("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ids) != 3 {
		t.Errorf("expected 3 IDs, got %d", len(ids))
	}
	if !ids["ep-2"] {
		t.Errorf("expected ep-2 in result")
	}
}

func TestGetUnconsolidatedEpisodeCount(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/episodes/count" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]int{"count": 42})
	})

	count, err := c.GetUnconsolidatedEpisodeCount()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 42 {
		t.Errorf("expected count 42, got %d", count)
	}
}

func TestAddEpisodeEdge(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/episodes/ep-from/edges" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["to_id"] != "ep-to" {
			t.Errorf("expected to_id 'ep-to', got %v", body["to_id"])
		}
		if body["edge_type"] != "follows" {
			t.Errorf("expected edge_type 'follows', got %v", body["edge_type"])
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.AddEpisodeEdge("ep-from", "ep-to", "follows", 1.0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetEpisodeSummariesBatch(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/episodes/summaries" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		ids := body["episode_ids"].([]any)
		if len(ids) != 2 {
			t.Errorf("expected 2 episode_ids, got %d", len(ids))
		}
		result := map[string]string{"ep-1": "summary one", "ep-2": "summary two"}
		json.NewEncoder(w).Encode(result)
	})

	summaries, err := c.GetEpisodeSummariesBatch([]string{"ep-1", "ep-2"}, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summaries["ep-1"] != "summary one" {
		t.Errorf("unexpected summary for ep-1: %q", summaries["ep-1"])
	}
}

func TestListEntities(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/entities" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("type") != "PERSON" {
			t.Errorf("expected type=PERSON, got %q", r.URL.Query().Get("type"))
		}
		entities := []*Entity{{ID: "ent-1", Name: "Alice", Type: "PERSON"}}
		json.NewEncoder(w).Encode(entities)
	})

	entities, err := c.ListEntities("PERSON", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entities) != 1 || entities[0].Name != "Alice" {
		t.Errorf("unexpected entities: %+v", entities)
	}
}

func TestConsolidate(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/consolidate" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ConsolidateResult{TracesCreated: 3, DurationMS: 250})
	})

	result, err := c.Consolidate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TracesCreated != 3 {
		t.Errorf("expected 3 traces created, got %d", result.TracesCreated)
	}
}

func TestDecayActivation(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/activation/decay" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["lambda"] != 0.1 {
			t.Errorf("expected lambda 0.1, got %v", body["lambda"])
		}
		json.NewEncoder(w).Encode(DecayResult{Updated: 10})
	})

	result, err := c.DecayActivation(0.1, 0.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Updated != 10 {
		t.Errorf("expected 10 updated, got %d", result.Updated)
	}
}

func TestReset(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		if r.URL.Path != "/v1/memory/reset" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Reset(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlush(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/memory/flush" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Flush(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAPIError_WithCode(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(apiError{Error: "invalid content", Code: "INVALID_CONTENT"})
	})

	_, err := c.IngestEpisode(IngestEpisodeRequest{Content: ""})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Should include the code in the error message
	errStr := err.Error()
	if len(errStr) == 0 {
		t.Error("error message is empty")
	}
}

func TestGetActivatedTraces(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("threshold") != "0.5" {
			t.Errorf("expected threshold=0.5, got %q", r.URL.Query().Get("threshold"))
		}
		if r.URL.Query().Get("limit") != "20" {
			t.Errorf("expected limit=20, got %q", r.URL.Query().Get("limit"))
		}
		traces := []*Trace{{ID: "tr-hot", Activation: 0.9}}
		json.NewEncoder(w).Encode(traces)
	})

	traces, err := c.GetActivatedTraces(0.5, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(traces) != 1 || traces[0].Activation != 0.9 {
		t.Errorf("unexpected traces: %+v", traces)
	}
}

func TestReinforceTrace(t *testing.T) {
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/engrams/tr-reinforce/reinforce" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["alpha"] != 0.3 {
			t.Errorf("expected alpha=0.3, got %v", body["alpha"])
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.ReinforceTrace("tr-reinforce", nil, 0.3); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestTimestampRoundtrip verifies that time.Time fields survive JSON encode/decode.
func TestTimestampRoundtrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		ep := Episode{
			ID:             "ep-ts",
			Content:        "timestamp test",
			TimestampEvent: now,
			CreatedAt:      now,
		}
		json.NewEncoder(w).Encode(ep)
	})

	ep, err := c.GetEpisode("ep-ts")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ep.TimestampEvent.Equal(now) {
		t.Errorf("TimestampEvent roundtrip failed: got %v, want %v", ep.TimestampEvent, now)
	}
}
