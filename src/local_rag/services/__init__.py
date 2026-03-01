"""Service layer for local-rag.

Wraps existing business logic with thread-safety, progress callbacks,
and lifecycle management for use by both CLI and GUI.
"""

from local_rag.services.config_service import ConfigService
from local_rag.services.indexing_service import IndexingService
from local_rag.services.mcp_service import MCPService
from local_rag.services.status_service import StatusService

__all__ = ["ConfigService", "IndexingService", "MCPService", "StatusService"]
