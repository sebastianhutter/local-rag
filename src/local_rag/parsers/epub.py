"""EPUB text extraction using zipfile + BeautifulSoup."""

import logging
import xml.etree.ElementTree as ET
import zipfile
from pathlib import Path, PurePosixPath

from bs4 import BeautifulSoup

logger = logging.getLogger(__name__)


def parse_epub(path: Path) -> list[tuple[int, str]]:
    """Extract text from an EPUB file, chapter by chapter.

    Opens the EPUB as a ZIP archive, reads the OPF manifest to find the
    spine order, then extracts text from each XHTML content document.

    Args:
        path: Path to the EPUB file.

    Returns:
        List of (chapter_number, text) tuples (1-based),
        same shape as parse_pdf() for consistency.
    """
    if not path.exists():
        logger.error("EPUB file not found: %s", path)
        return []

    try:
        return _extract_chapters(path)
    except zipfile.BadZipFile:
        logger.error("EPUB is not a valid ZIP archive: %s", path)
        return []
    except Exception as e:
        logger.error("Failed to parse EPUB %s: %s", path, e)
        return []


def _extract_chapters(path: Path) -> list[tuple[int, str]]:
    """Extract chapter text from an EPUB file."""
    with zipfile.ZipFile(path, "r") as zf:
        # Step 1: Find the OPF file via META-INF/container.xml
        opf_path = _find_opf_path(zf)
        if not opf_path:
            logger.warning("Could not find OPF file in EPUB: %s", path)
            return []

        # Step 2: Parse OPF to get spine item hrefs
        opf_dir = str(PurePosixPath(opf_path).parent)
        spine_hrefs = _parse_opf_spine(zf, opf_path)
        if not spine_hrefs:
            logger.warning("No spine items found in EPUB: %s", path)
            return []

        # Step 3: Extract text from each spine item
        chapters: list[tuple[int, str]] = []
        for chapter_num, href in enumerate(spine_hrefs, start=1):
            # Resolve href relative to OPF directory
            if opf_dir and opf_dir != ".":
                full_path = f"{opf_dir}/{href}"
            else:
                full_path = href

            try:
                content = zf.read(full_path).decode("utf-8", errors="replace")
            except KeyError:
                logger.debug("Spine item not found in archive: %s", full_path)
                continue

            text = _html_to_text(content)
            if text.strip():
                chapters.append((chapter_num, text.strip()))

        if not chapters:
            logger.warning("No extractable text found in EPUB %s (may be DRM-protected)", path)

        return chapters


def _find_opf_path(zf: zipfile.ZipFile) -> str | None:
    """Find the OPF file path from META-INF/container.xml."""
    try:
        container_xml = zf.read("META-INF/container.xml").decode("utf-8")
    except KeyError:
        return None

    root = ET.fromstring(container_xml)
    # Handle namespace
    ns = {"container": "urn:oasis:names:tc:opendocument:xmlns:container"}
    rootfile = root.find(".//container:rootfile", ns)
    if rootfile is not None:
        return rootfile.get("full-path")

    # Fallback: try without namespace
    for elem in root.iter():
        if elem.tag.endswith("rootfile"):
            return elem.get("full-path")

    return None


def _parse_opf_spine(zf: zipfile.ZipFile, opf_path: str) -> list[str]:
    """Parse the OPF file and return ordered list of content document hrefs from the spine."""
    try:
        opf_content = zf.read(opf_path).decode("utf-8")
    except KeyError:
        return []

    root = ET.fromstring(opf_content)

    # Detect OPF namespace
    ns_opf = ""
    if root.tag.startswith("{"):
        ns_opf = root.tag.split("}")[0] + "}"

    # Build manifest: id -> href
    manifest: dict[str, str] = {}
    for item in root.iter(f"{ns_opf}item"):
        item_id = item.get("id", "")
        href = item.get("href", "")
        media_type = item.get("media-type", "")
        if item_id and href:
            manifest[item_id] = (href, media_type)

    # Read spine order
    spine_hrefs: list[str] = []
    for itemref in root.iter(f"{ns_opf}itemref"):
        idref = itemref.get("idref", "")
        if idref in manifest:
            href, media_type = manifest[idref]
            # Only include XHTML/HTML content documents
            if "html" in media_type or "xml" in media_type:
                spine_hrefs.append(href)

    return spine_hrefs


def _html_to_text(html_content: str) -> str:
    """Convert HTML/XHTML content to plain text."""
    soup = BeautifulSoup(html_content, "html.parser")
    text = soup.get_text(separator="\n")
    # Clean up excessive blank lines
    lines = [line.strip() for line in text.splitlines()]
    return "\n".join(line for line in lines if line)
