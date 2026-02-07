"""Ollama embedding helpers for local-rag."""

import logging
import struct

import httpx
import ollama

from local_rag.config import Config

logger = logging.getLogger(__name__)

# Send at most this many texts per Ollama API call to avoid timeouts
_BATCH_SIZE = 32

# Per-request timeout in seconds — generous because the first call
# triggers model loading which can take minutes on large models
_TIMEOUT = 300.0


class OllamaConnectionError(Exception):
    """Raised when Ollama is not reachable."""


def _client() -> ollama.Client:
    """Create an Ollama client with a finite timeout."""
    return ollama.Client(timeout=httpx.Timeout(_TIMEOUT))


def _raise_if_connection_error(e: Exception) -> None:
    """Re-raise as OllamaConnectionError if the error looks like a connection issue."""
    msg = str(e).lower()
    if "connect" in msg or "refused" in msg:
        raise OllamaConnectionError(
            "Cannot connect to Ollama. Is it running? Start with: ollama serve"
        ) from e


def get_embedding(text: str, config: Config) -> list[float]:
    """Get embedding for a single text.

    Args:
        text: Text to embed.
        config: Application configuration.

    Returns:
        Embedding vector as list of floats.

    Raises:
        OllamaConnectionError: If Ollama is not running or unreachable.
    """
    try:
        response = _client().embed(model=config.embedding_model, input=text)
        return response["embeddings"][0]
    except Exception as e:
        _raise_if_connection_error(e)
        raise


def get_embeddings(texts: list[str], config: Config) -> list[list[float]]:
    """Get embeddings for a batch of texts.

    Sends texts in sub-batches to avoid timeouts on large inputs.
    Logs progress for visibility.

    Args:
        texts: List of texts to embed.
        config: Application configuration.

    Returns:
        List of embedding vectors.

    Raises:
        OllamaConnectionError: If Ollama is not running or unreachable.
    """
    if not texts:
        return []

    client = _client()
    all_embeddings: list[list[float]] = []

    for start in range(0, len(texts), _BATCH_SIZE):
        batch = texts[start : start + _BATCH_SIZE]
        if len(texts) > _BATCH_SIZE:
            logger.info(
                "Embedding batch %d-%d of %d texts...",
                start + 1,
                min(start + _BATCH_SIZE, len(texts)),
                len(texts),
            )
        try:
            response = client.embed(model=config.embedding_model, input=batch)
            all_embeddings.extend(response["embeddings"])
        except Exception as e:
            _raise_if_connection_error(e)
            raise

    return all_embeddings


def serialize_float32(vec: list[float]) -> bytes:
    """Serialize a float vector to sqlite-vec binary format.

    Args:
        vec: Embedding vector as list of floats.

    Returns:
        Packed binary representation for sqlite-vec.
    """
    return struct.pack(f"{len(vec)}f", *vec)
