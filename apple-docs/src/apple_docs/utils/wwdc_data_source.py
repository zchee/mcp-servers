import json
import logging
from pathlib import Path

import aiofiles

from apple_docs.types.wwdc import GlobalMetadata, TopicIndex, WWDCVideo, YearIndex


logger = logging.getLogger(__name__)

WWDC_DATA_DIR = Path(__file__).parent.parent / "data" / "wwdc"

# Caches
_global_metadata_cache: GlobalMetadata | None = None
_all_videos_cache: list[WWDCVideo] | None = None


async def read_bundled_file(file_path: str) -> str:
    """
    Read a file from the bundled WWDC data directory.

    Args:
        file_path: Relative path to the file within the data directory.

    Returns:
        The content of the file as a string.

    Raises:
        Exception: If the file cannot be read.
    """
    full_path = WWDC_DATA_DIR / file_path
    try:
        async with aiofiles.open(full_path, encoding="utf-8") as f:
            return await f.read()
    except Exception as e:
        logger.error(f"Failed to read bundled data: {file_path}", exc_info=True)
        raise Exception(f"Failed to load WWDC data from {file_path}: {str(e)}") from e


async def load_global_metadata() -> GlobalMetadata:
    """
    Load the global index.json metadata.

    Returns:
        The global metadata object.

    Raises:
        Exception: If the metadata cannot be loaded.
    """
    global _global_metadata_cache
    if _global_metadata_cache:
        return _global_metadata_cache

    try:
        data = await read_bundled_file("index.json")
        _global_metadata_cache = json.loads(data)
        return _global_metadata_cache  # type: ignore
    except Exception as e:
        logger.error("Failed to load global metadata", exc_info=True)
        raise Exception("Failed to load WWDC metadata.") from e


async def load_topic_index(topic_id: str) -> TopicIndex:
    """
    Load the index for a specific topic.

    Args:
        topic_id: The ID of the topic.

    Returns:
        The topic index object.

    Raises:
        Exception: If the topic index cannot be loaded.
    """
    try:
        data = await read_bundled_file(f"by-topic/{topic_id}/index.json")
        return json.loads(data)
    except Exception as e:
        logger.error(f"Failed to load topic index: {topic_id}", exc_info=True)
        raise Exception(f"Topic not found: {topic_id}") from e


async def load_year_index(year: str) -> YearIndex:
    """
    Load the index for a specific year.

    Args:
        year: The year (e.g., "2024").

    Returns:
        The year index object.

    Raises:
        Exception: If the year index cannot be loaded.
    """
    try:
        data = await read_bundled_file(f"by-year/{year}/index.json")
        return json.loads(data)
    except Exception as e:
        logger.error(f"Failed to load year index: {year}", exc_info=True)
        raise Exception(f"Year not found: {year}") from e


async def load_video_data(year: str, video_id: str) -> WWDCVideo:
    """
    Load detailed data for a specific video.

    Args:
        year: The year of the video.
        video_id: The ID of the video.

    Returns:
        The video data object.

    Raises:
        Exception: If the video data cannot be loaded.
    """
    try:
        data = await read_bundled_file(f"videos/{year}-{video_id}.json")
        return json.loads(data)
    except Exception as e:
        logger.error(f"Failed to load video: {year}-{video_id}", exc_info=True)
        raise Exception(f"Video not found: {year}-{video_id}") from e


async def load_all_videos() -> list[WWDCVideo]:
    """
    Load the list of all WWDC videos.

    Returns:
        A list of all WWDC videos.

    Raises:
        Exception: If the video list cannot be loaded.
    """
    global _all_videos_cache
    if _all_videos_cache:
        return _all_videos_cache

    try:
        data = await read_bundled_file("all-videos.json")
        _all_videos_cache = json.loads(data)
        return _all_videos_cache  # type: ignore
    except Exception as e:
        logger.error("Failed to load all videos", exc_info=True)
        raise Exception("Failed to load WWDC video list") from e


def is_data_available() -> bool:
    """
    Check if the WWDC data is available locally.

    Returns:
        True if the data directory and index.json exist, False otherwise.
    """
    return WWDC_DATA_DIR.exists() and (WWDC_DATA_DIR / "index.json").exists()
