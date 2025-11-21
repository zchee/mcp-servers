import contextlib
import logging
from pathlib import Path

import aiofiles
import orjson

from apple_docs.types.wwdc import GlobalMetadata, TopicIndex, TopicInfo, WWDCVideo, YearIndex


logger = logging.getLogger(__name__)

WWDC_DATA_DIR = Path(__file__).parent.parent / "data" / "wwdc"

# Caches
_global_metadata_cache: GlobalMetadata | None = None
_all_videos_cache: list[WWDCVideo] | None = None
_video_data_cache: dict[str, WWDCVideo] = {}
VIDEO_CACHE_SIZE = 200

# Indices
_videos_by_year_cache: dict[str, list[WWDCVideo]] = {}
_videos_by_topic_cache: dict[str, list[WWDCVideo]] = {}
_topics_cache: dict[str, TopicInfo] = {}


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


def _prune_cache(cache: dict[str, WWDCVideo]) -> None:
    """Ensure cache size stays within limits."""
    if len(cache) >= VIDEO_CACHE_SIZE:
        with contextlib.suppress(StopIteration):
            cache.pop(next(iter(cache)))


async def load_global_metadata() -> GlobalMetadata:
    """
    Load the global index.json metadata.

    Returns:
        The global metadata object.

    Raises:
        Exception: If the metadata cannot be loaded.
    """
    global _global_metadata_cache, _topics_cache
    if _global_metadata_cache:
        return _global_metadata_cache

    try:
        data = await read_bundled_file("index.json")
        _global_metadata_cache = orjson.loads(data)

        # Build topics cache
        if _global_metadata_cache:
             _topics_cache = {t["id"]: t for t in _global_metadata_cache["topics"]}

        return _global_metadata_cache  # type: ignore
    except Exception as e:
        logger.error("Failed to load global metadata", exc_info=True)
        raise Exception("Failed to load WWDC metadata.") from e


async def get_topic_by_id(topic_id: str) -> TopicInfo | None:
    """
    Get a topic by its ID.

    Args:
        topic_id: The ID of the topic.

    Returns:
        The topic info, or None if not found.
    """
    await load_global_metadata()
    return _topics_cache.get(topic_id)


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
        return orjson.loads(data)
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
        return orjson.loads(data)
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
    cache_key = f"{year}-{video_id}"
    if cache_key in _video_data_cache:
        return _video_data_cache[cache_key]

    try:
        data = await read_bundled_file(f"videos/{year}-{video_id}.json")
        video: WWDCVideo = orjson.loads(data)
        _prune_cache(_video_data_cache)
        _video_data_cache[cache_key] = video
        return video
    except Exception as e:
        logger.error(f"Failed to load video: {year}-{video_id}", exc_info=True)
        raise Exception(f"Video not found: {year}-{video_id}") from e


async def load_all_videos() -> list[WWDCVideo]:
    """
    Load the list of all WWDC videos and build indices.

    Returns:
        A list of all WWDC videos.

    Raises:
        Exception: If the video list cannot be loaded.
    """
    global _all_videos_cache, _videos_by_year_cache, _videos_by_topic_cache
    if _all_videos_cache:
        return _all_videos_cache

    try:
        data = await read_bundled_file("all-videos.json")
        # all-videos.json has structure {"videos": [...]}
        parsed = orjson.loads(data)
        videos: list[WWDCVideo] = parsed.get("videos", [])
        # Sort videos by year descending to prioritize recent content
        videos.sort(key=lambda x: x.get("year", ""), reverse=True)
        _all_videos_cache = videos

        # Build indices
        _videos_by_year_cache = {}
        _videos_by_topic_cache = {}

        for video in videos:
            year = video["year"]
            if year not in _videos_by_year_cache:
                _videos_by_year_cache[year] = []
            _videos_by_year_cache[year].append(video)

            for topic in video.get("topics", []):
                if topic not in _videos_by_topic_cache:
                    _videos_by_topic_cache[topic] = []
                _videos_by_topic_cache[topic].append(video)

        return _all_videos_cache  # type: ignore
    except Exception as e:
        logger.error("Failed to load all videos", exc_info=True)
        raise Exception("Failed to load WWDC video list") from e


async def get_videos_by_year(year: str) -> list[WWDCVideo]:
    """Get videos for a specific year using the in-memory index."""
    await load_all_videos()  # Ensure data is loaded
    return _videos_by_year_cache.get(year, [])


async def get_videos_by_topic(topic: str) -> list[WWDCVideo]:
    """Get videos for a specific topic using the in-memory index."""
    await load_all_videos()  # Ensure data is loaded
    return _videos_by_topic_cache.get(topic, [])


def is_data_available() -> bool:
    """
    Check if the WWDC data is available locally.

    Returns:
        True if the data directory and index.json exist, False otherwise.
    """
    return WWDC_DATA_DIR.exists() and (WWDC_DATA_DIR / "index.json").exists()
