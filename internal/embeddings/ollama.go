// Package embeddings provides Ollama embedding helpers for local-rag.
package embeddings

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

// hostProbeTimeout bounds each reachability/model check when resolving a host.
const hostProbeTimeout = 2 * time.Second

const (
	// BatchSize is the maximum number of texts per Ollama API call.
	BatchSize = 32
	// Timeout is the per-request timeout — generous because the first call
	// may trigger model loading.
	Timeout = 300 * time.Second
)

// OllamaConnectionError indicates that Ollama is not reachable.
type OllamaConnectionError struct {
	Err error
}

func (e *OllamaConnectionError) Error() string {
	return fmt.Sprintf("cannot connect to Ollama: %v. Is it running? Start with: ollama serve", e.Err)
}

func (e *OllamaConnectionError) Unwrap() error { return e.Err }

func isConnectionError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connect") || strings.Contains(msg, "refused")
}

// ResolveHost selects which Ollama server the embedding calls will use and
// exports it via the OLLAMA_HOST environment variable (which api.Client reads).
//
// Precedence:
//  1. An OLLAMA_HOST already set in the environment is treated as an explicit
//     override and wins — probing is skipped.
//  2. Otherwise each host in hosts is tried in order; the first that is
//     reachable AND already serves `model` is chosen. Requiring the model
//     prevents selecting a host that would produce embeddings from a different
//     model (which would be inconsistent with the existing corpus).
//  3. If none qualify, the environment is left unchanged and Ollama's default
//     (http://127.0.0.1:11434) applies.
//
// Hosts may be given as "host:port" or "http://host:port".
func ResolveHost(hosts []string, model string) {
	if h := os.Getenv("OLLAMA_HOST"); h != "" {
		slog.Info("using OLLAMA_HOST from environment", "host", h)
		return
	}
	for _, h := range hosts {
		base := normalizeHost(h)
		if hostHasModel(base, model) {
			os.Setenv("OLLAMA_HOST", base)
			slog.Info("resolved ollama host", "host", base, "model", model)
			return
		}
		slog.Warn("ollama host unavailable or missing model, trying next",
			"host", base, "model", model)
	}
	if len(hosts) > 0 {
		slog.Warn("no configured embedding host is reachable with the model; using Ollama default")
	}
}

// normalizeHost ensures a host string has an http(s) scheme.
func normalizeHost(host string) string {
	host = strings.TrimRight(strings.TrimSpace(host), "/")
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return host
}

// hostHasModel reports whether the Ollama server at base is reachable within the
// probe timeout and has `model` available (matched by exact name or family,
// e.g. "bge-m3" matches "bge-m3:latest").
func hostHasModel(base, model string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), hostProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	var payload struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false
	}
	for _, m := range payload.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			return true
		}
	}
	return false
}

func newClient() (*api.Client, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("create ollama client: %w", err)
	}
	return client, nil
}

// GetEmbedding returns the embedding vector for a single text.
func GetEmbedding(ctx context.Context, text, model string) ([]float32, error) {
	results, err := GetEmbeddings(ctx, []string{text}, model)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}
	return results[0], nil
}

// GetEmbeddings returns embedding vectors for a batch of texts,
// splitting into sub-batches of BatchSize to avoid timeouts.
func GetEmbeddings(ctx context.Context, texts []string, model string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	client, err := newClient()
	if err != nil {
		return nil, err
	}

	all := make([][]float32, 0, len(texts))

	for start := 0; start < len(texts); start += BatchSize {
		end := start + BatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		if len(texts) > BatchSize {
			slog.Info("embedding batch",
				"from", start+1,
				"to", end,
				"total", len(texts),
			)
		}

		reqCtx, cancel := context.WithTimeout(ctx, Timeout)
		resp, err := client.Embed(reqCtx, &api.EmbedRequest{
			Model: model,
			Input: batch,
		})
		cancel()

		if err != nil {
			if isConnectionError(err) {
				return nil, &OllamaConnectionError{Err: err}
			}
			return nil, fmt.Errorf("embed batch [%d:%d]: %w", start, end, err)
		}

		all = append(all, resp.Embeddings...)
	}

	return all, nil
}

// SerializeFloat32 converts a float32 embedding vector to the packed binary
// format expected by sqlite-vec.
func SerializeFloat32(vec []float32) []byte {
	buf := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}

// DeserializeFloat32 converts a packed sqlite-vec binary blob back into a
// float32 vector. It is the inverse of SerializeFloat32.
func DeserializeFloat32(buf []byte) []float32 {
	vec := make([]float32, len(buf)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
	}
	return vec
}
