# Hybrid Search and Reciprocal Rank Fusion (RRF)

This document explains how local-rag finds relevant documents when you run a search query. It covers the two search strategies, why we use both, and how results are combined into a single ranked list.

## The Problem with a Single Search Strategy

There are two fundamentally different ways to search text:

**Keyword search** finds documents that contain the exact words you typed. If you search for "kubernetes deployment", it returns documents containing those words. It's fast and precise, but misses synonyms and rephrased content. A document about "k8s rollout strategy" won't match even though it's clearly relevant.

**Semantic search** converts your query and all stored documents into numerical vectors (lists of numbers) that represent their meaning. It then finds documents whose vectors are closest to your query's vector. This catches synonyms and rephrasings, but can miss documents that contain the exact phrase you need — and sometimes returns results that are thematically related but not actually useful.

Neither approach alone gives reliably good results. Hybrid search runs both and combines them.

## How local-rag Runs a Search

When you search for "kubernetes deployment strategy", two things happen in parallel:

### 1. Vector Search (Semantic)

Your query text is sent to Ollama, which runs the bge-m3 model locally to produce a 1024-dimensional vector — a list of 1024 floating-point numbers that encode the meaning of your query.

This vector is then compared against all stored document vectors using sqlite-vec, a SQLite extension for nearest-neighbor search. sqlite-vec returns the closest documents ranked by cosine distance (lower distance = more similar meaning).

The result is a ranked list like:

| Rank | Document | Why it matched |
|------|----------|---------------|
| 1 | "K8s rollout best practices" | Similar meaning |
| 2 | "Container orchestration guide" | Related topic |
| 3 | "Kubernetes deployment YAML reference" | Direct match |

### 2. Full-Text Search (Keyword)

The same query text is tokenized and matched against an FTS5 index (SQLite's built-in full-text search engine). FTS5 finds documents containing the literal words "kubernetes", "deployment", and "strategy", ranked by how well they match (BM25 scoring internally).

The result is a different ranked list:

| Rank | Document | Why it matched |
|------|----------|---------------|
| 1 | "Kubernetes deployment YAML reference" | Contains exact words |
| 2 | "Kubernetes cluster deployment checklist" | Contains exact words |
| 3 | "Deployment strategy for microservices" | Partial word match |

Notice the two lists overlap but aren't identical. Each catches things the other misses.

## Combining Results with Reciprocal Rank Fusion

Now we have two ranked lists. The question is: how do we merge them into one?

Simple approaches like averaging raw scores don't work well because vector distances and FTS5 rank scores are on completely different scales and have different distributions. A vector distance of 0.3 and an FTS rank of -12.5 aren't meaningfully comparable.

**Reciprocal Rank Fusion (RRF)** solves this by ignoring the raw scores entirely and using only the rank positions. The intuition: if a document ranks highly in both lists, it should rank highly in the merged list. If it ranks highly in only one list, it should still appear but lower.

### The Formula

For each document, RRF computes:

```
rrf_score = vector_weight / (k + vector_rank) + fts_weight / (k + fts_rank)
```

Where:
- `vector_rank` is the document's position in the vector search results (1 = best match)
- `fts_rank` is the document's position in the FTS results (1 = best match)
- `k` is a smoothing constant (default: 60)
- `vector_weight` is how much to trust semantic search (default: 0.7)
- `fts_weight` is how much to trust keyword search (default: 0.3)

If a document only appears in one of the two lists, it only gets a score from that list (the other term is zero).

### Worked Example

Say we have these results:

**Vector search results:**
1. Doc A (k8s rollout best practices)
2. Doc B (container orchestration guide)
3. Doc C (kubernetes deployment YAML reference)

**FTS results:**
1. Doc C (kubernetes deployment YAML reference)
2. Doc D (kubernetes cluster deployment checklist)
3. Doc E (deployment strategy for microservices)

Using the defaults (`k=60`, `vector_weight=0.7`, `fts_weight=0.3`):

| Document | Vector Rank | FTS Rank | Vector Contribution | FTS Contribution | Total RRF Score |
|----------|-------------|----------|---------------------|------------------|-----------------|
| Doc C | 3 | 1 | 0.7 / (60+3) = 0.0111 | 0.3 / (60+1) = 0.0049 | **0.0160** |
| Doc A | 1 | — | 0.7 / (60+1) = 0.0115 | 0 | **0.0115** |
| Doc B | 2 | — | 0.7 / (60+2) = 0.0113 | 0 | **0.0113** |
| Doc D | — | 2 | 0 | 0.3 / (60+2) = 0.0048 | **0.0048** |
| Doc E | — | 3 | 0 | 0.3 / (60+3) = 0.0048 | **0.0048** |

**Final ranking:** C, A, B, D, E

Doc C wins because it appeared in both lists — a strong signal that it's relevant both semantically and by keyword. Doc A ranks second because it was the top semantic match even though it didn't contain the exact keywords.

### Why k = 60?

The `k` parameter controls how much the ranking position matters. With a higher `k`, the difference between rank 1 and rank 5 shrinks — the formula becomes more forgiving of lower-ranked results. With a lower `k`, top-ranked documents get disproportionately more weight.

`k=60` is the standard value from the original RRF paper (Cormack et al., 2009). It works well across a wide range of datasets and rarely needs tuning.

### Why 0.7 / 0.3 Weights?

The default weights favor semantic search (0.7) over keyword search (0.3). This reflects the typical use case: most queries are natural language questions where meaning matters more than exact words. If you primarily search for exact phrases or identifiers, you could increase `fts_weight` in the config.

These values are configurable in `~/.local-rag/config.json`:

```json
{
  "search_defaults": {
    "top_k": 10,
    "rrf_k": 60,
    "vector_weight": 0.7,
    "fts_weight": 0.3
  }
}
```

## Implementation Reference

The search pipeline lives in `src/local_rag/search.py`:

- `_vector_search()` — runs the sqlite-vec nearest-neighbor query
- `_fts_search()` — runs the FTS5 keyword query
- `rrf_merge()` — combines both ranked lists using the formula above
- `search()` — orchestrates the full pipeline: run both searches, merge, apply filters, fetch full document data

All filtering (by collection, source type, date range, sender) happens after the initial search but before the final ranking, so filters don't interfere with the ranking logic itself.
