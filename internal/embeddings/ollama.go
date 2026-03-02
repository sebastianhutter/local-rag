// Package embeddings provides Ollama embedding helpers for local-rag.
package embeddings

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

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
