package embeddings

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// tagsServer returns an httptest server whose /api/tags advertises the given models.
func tagsServer(models ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := `{"models":[`
		for i, m := range models {
			if i > 0 {
				out += ","
			}
			out += `{"name":"` + m + `"}`
		}
		out += `]}`
		w.Write([]byte(out))
	}))
}

func TestResolveHost_PicksReachableWithModel(t *testing.T) {
	missing := tagsServer("qwen3:4b") // reachable but no bge-m3
	defer missing.Close()
	good := tagsServer("bge-m3:latest")
	defer good.Close()

	t.Setenv("OLLAMA_HOST", "") // treat as unset
	ResolveHost([]string{missing.URL, good.URL}, "bge-m3")

	if got := os.Getenv("OLLAMA_HOST"); got != good.URL {
		t.Errorf("OLLAMA_HOST = %q, want %q (should skip the host missing the model)", got, good.URL)
	}
}

func TestResolveHost_HonorsExplicitEnv(t *testing.T) {
	good := tagsServer("bge-m3:latest")
	defer good.Close()

	t.Setenv("OLLAMA_HOST", "http://explicit:11434")
	ResolveHost([]string{good.URL}, "bge-m3")

	if got := os.Getenv("OLLAMA_HOST"); got != "http://explicit:11434" {
		t.Errorf("OLLAMA_HOST = %q, want the explicit override to be preserved", got)
	}
}

func TestResolveHost_NoneReachableLeavesUnset(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	// Unroutable/closed port + a reachable server that lacks the model.
	bad := tagsServer("qwen3:4b")
	defer bad.Close()
	ResolveHost([]string{"http://127.0.0.1:1", bad.URL}, "bge-m3")

	if got := os.Getenv("OLLAMA_HOST"); got != "" {
		t.Errorf("OLLAMA_HOST = %q, want empty (no suitable host)", got)
	}
}

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"node-b:11434":         "http://node-b:11434",
		"http://node-b:11434":  "http://node-b:11434",
		"https://x:443/":       "https://x:443",
		"192.168.30.90:11434/": "http://192.168.30.90:11434",
	}
	for in, want := range cases {
		if got := normalizeHost(in); got != want {
			t.Errorf("normalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}
