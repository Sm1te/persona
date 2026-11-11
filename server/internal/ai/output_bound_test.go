package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestResponsesBoundOutputIsOptInAndNeverDropped(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		calls := atomic.Int32{}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if bounded {
				if body["max_output_tokens"] != float64(256) {
					t.Errorf("missing bound: %+v", body)
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":{"message":"unsupported output bound"}}`)
			} else {
				if _, present := body["max_output_tokens"]; present {
					t.Error("legacy caller changed")
				}
				w.Header().Set("content-type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\"}\n\n")
			}
		}))
		_, err := New(Config{API: "responses", BaseURL: srv.URL, APIKey: "fixture", Model: "fixture"}).Chat(context.Background(), Request{MaxTokens: 256, BoundOutput: bounded})
		srv.Close()
		if bounded && err == nil {
			t.Error("bound refusal must fail closed")
		}
		if !bounded && err != nil {
			t.Error(err)
		}
		if calls.Load() != 1 {
			t.Fatal("a refused bound was retried without the bound")
		}
	}
}
