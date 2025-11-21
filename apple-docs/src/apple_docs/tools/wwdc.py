import asyncio
from collections.abc import Awaitable
import re
from typing import Any

from apple_docs.types.wwdc import CodeExample, WWDCVideo
from apple_docs.utils.wwdc_data_source import (
    get_topic_by_id,
    get_videos_by_topic,
    get_videos_by_year,
    load_all_videos,
    load_global_metadata,
    load_video_data,
)


MAX_FULL_SEARCH_CANDIDATES = 50


async def load_videos_details(videos: list[tuple[str, str]]) -> list[WWDCVideo]:
    """
    Load detailed data for a list of videos.

    Args:
        videos: List of (year, video_id) tuples.

    Returns:
        List of WWDCVideo objects.
    """
    sem = asyncio.Semaphore(20)

    async def load_with_sem(year: str, video_id: str) -> WWDCVideo:
        async with sem:
            return await load_video_data(year, video_id)

    tasks: list[Awaitable[WWDCVideo]] = []
    for year, video_id in videos:
        tasks.append(load_with_sem(year, video_id))

    results = await asyncio.gather(*tasks, return_exceptions=True)
    videos_list: list[WWDCVideo] = []
    for res in results:
        if isinstance(res, dict):
            videos_list.append(res)
    return videos_list


async def list_wwdc_videos(
    year: str | None = None, topic: str | None = None, has_code: bool | None = None, limit: int = 50
) -> str:
    """
    List WWDC videos with filtering options.

    Args:
        year: Filter by year (e.g., "2024", "2023"). Use "all" for all years.
        topic: Filter by topic name or ID.
        has_code: Filter videos that have code examples.
        limit: Max results. Default: 50.

    Returns:
        Formatted string containing the list of videos.
    """
    try:
        if year and year != "all":
            all_videos = await get_videos_by_year(year)
        else:
            all_videos = await load_all_videos()

        # Apply filters
        filtered_videos = all_videos

        if topic:
            topic_lower = topic.lower()
            filtered_videos = [
                v
                for v in filtered_videos
                if any(t.lower().find(topic_lower) != -1 for t in v.get("topics", []))
                or topic_lower in v.get("title", "").lower()
            ]

        if has_code is not None:
            filtered_videos = [v for v in filtered_videos if v.get("hasCode") == has_code]

        limited_videos = filtered_videos[:limit]

        # Note: all_videos.json contains summary data.
        # If we needed full details (speakers, etc), we would need to load individual files here.
        # For listing, summary is usually sufficient.

        return format_video_list(limited_videos, year, topic, has_code)

    except Exception as e:
        return f"Error: Failed to list WWDC videos: {str(e)}"


async def search_wwdc_content(
    query: str,
    search_in: str = "both",  # 'transcript', 'code', 'both'
    year: str | None = None,
    language: str | None = None,
    limit: int = 20,
) -> str:
    """
    Search within WWDC video transcripts and code examples.

    Args:
        query: Search query.
        search_in: Scope of search: "transcript", "code", or "both". Default: "both".
        year: Filter by year.
        language: Filter code examples by language (e.g., "swift", "objc").
        limit: Max results. Default: 20.

    Returns:
        Formatted string containing search results.
    """
    try:
        query_lower = query.lower()
        results: list[dict[str, Any]] = []

        if year and year != "all":
            all_videos = await get_videos_by_year(year)
        else:
            all_videos = await load_all_videos()

        metadata_matches: list[WWDCVideo] = []
        content_candidates: list[WWDCVideo] = []

        for v in all_videos:
            title_match = query_lower in v.get("title", "").lower()
            topic_match = any(query_lower in t.lower() for t in v.get("topics", []))

            if title_match or topic_match:
                metadata_matches.append(v)
            elif (
                (search_in in ["code", "both"] and v.get("hasCode"))
                or (search_in in ["transcript", "both"] and v.get("hasTranscript"))
            ):
                content_candidates.append(v)

        # Prioritize metadata matches, then content candidates, up to the limit
        potential_videos = metadata_matches + content_candidates
        if len(potential_videos) > MAX_FULL_SEARCH_CANDIDATES:
            potential_videos = potential_videos[:MAX_FULL_SEARCH_CANDIDATES]

        if not potential_videos:
            return format_search_results([], query, search_in)

        # Load full data for potential matches to search content
        videos_to_load: list[tuple[str, str]] = []
        for v in potential_videos:
            videos_to_load.append((v["year"], v["id"]))

        videos = await load_videos_details(videos_to_load)

        # Compile regex once
        pattern = re.compile(re.escape(query), re.IGNORECASE)

        for video in videos:
            matches: list[dict[str, Any]] = []

            # Search transcript
            transcript = video.get("transcript")
            if search_in in ["transcript", "both"] and transcript:
                transcript_matches = await asyncio.to_thread(search_in_transcript, transcript["fullText"], pattern)
                for m in transcript_matches:
                    matches.append({
                        "type": "transcript",
                        "context": m["context"],
                        "timestamp": m.get("timestamp"),
                    })

            # Search code
            code_examples: list[CodeExample] | None = video.get("codeExamples")
            if search_in in ["code", "both"] and code_examples:
                code_matches = await asyncio.to_thread(search_in_code, code_examples, pattern, language)
                for m in code_matches:
                    matches.append({"type": "code", "context": m["context"], "timestamp": m.get("timestamp")})

            if matches:
                results.append({
                    "video": video,
                    "matches": matches[:3],  # Max 3 matches per video
                })

        results.sort(key=lambda x: len(x["matches"]), reverse=True)
        limited_results = results[:limit]

        return format_search_results(limited_results, query, search_in)

    except Exception as e:
        return f"Error: Failed to search WWDC content: {str(e)}"


async def get_wwdc_video(year: str, video_id: str, include_transcript: bool = True, include_code: bool = True) -> str:
    """
    Get detailed information for a specific WWDC video.

    Args:
        year: The year of the video (e.g., "2024").
        video_id: The ID of the video (e.g., "10123").
        include_transcript: Include full transcript. Default: true.
        include_code: Include code examples. Default: true.

    Returns:
        Formatted string containing video details.
    """
    try:
        video = await load_video_data(year, video_id)
        return format_video_detail(video, include_transcript, include_code)
    except Exception as e:
        return f"Error: Failed to get WWDC video: {str(e)}"


async def get_wwdc_code_examples(
    framework: str | None = None,
    topic: str | None = None,
    year: str | None = None,
    language: str | None = None,
    limit: int = 30,
) -> str:
    """
    Extract code examples from WWDC videos.

    Args:
        framework: Filter by framework usage in code.
        topic: Filter by video topic.
        year: Filter by year.
        language: Filter by programming language.
        limit: Max results. Default: 30.

    Returns:
        Formatted string containing code examples.
    """
    try:
        code_examples: list[dict[str, Any]] = []

        if year and year != "all":
            all_videos = await get_videos_by_year(year)
        else:
            all_videos = await load_all_videos()

        potential_videos: list[WWDCVideo] = []

        topic_lower = topic.lower() if topic else None

        for v in all_videos:
            if not v.get("hasCode"):
                continue

            if topic_lower and not (
                any(topic_lower in t.lower() for t in v.get("topics", []))
                or topic_lower in v.get("title", "").lower()
            ):
                continue

            potential_videos.append(v)

        if not potential_videos:
            return format_code_examples([], framework, topic, language)

        # Load full data
        videos_to_load: list[tuple[str, str]] = []
        for v in potential_videos:
            videos_to_load.append((v["year"], v["id"]))

        videos = await load_videos_details(videos_to_load)

        for video in videos:
            video_code_examples = video.get("codeExamples")
            if not video_code_examples:
                continue

            for example in video_code_examples:
                if language and example["language"].lower() != language.lower():
                    continue

                if framework and framework.lower() not in example["code"].lower():
                    continue

                code_examples.append({
                    "code": example["code"],
                    "language": example["language"],
                    "title": example.get("title"),
                    "timestamp": example.get("timestamp"),
                    "videoTitle": video["title"],
                    "videoUrl": video["url"],
                    "year": video["year"],
                })

        limited_examples = code_examples[:limit]
        return format_code_examples(limited_examples, framework, topic, language)

    except Exception as e:
        return f"Error: Failed to get WWDC code examples: {str(e)}"


async def browse_wwdc_topics(
    topic_id: str | None = None, include_videos: bool = True, year: str | None = None, limit: int = 20
) -> str:
    """
    Browse WWDC topics and their associated videos.

    Args:
        topic_id: Specific topic ID to view. Leave empty to list all topics.
        include_videos: Include videos in the topic details. Default: true.
        year: Filter videos by year.
        limit: Max videos to show per topic. Default: 20.

    Returns:
        Formatted string containing topic details or list of topics.
    """
    try:
        metadata = await load_global_metadata()

        if not topic_id:
            content = "# WWDC Topics\n\n"
            content += f"Found {len(metadata['topics'])} topics:\n\n"

            for t in metadata["topics"]:
                content += f"## [{t['name']}]({t['url']})\n"
                content += f"**Topic ID:** {t['id']}\n"

                stats = metadata["statistics"]["byTopic"].get(t["id"])
                if stats:
                    content += f"**Videos:** {stats}\n"
                content += "\n"
            return content

        topic = await get_topic_by_id(topic_id)
        if not topic:
            return f'Topic "{topic_id}" not found. Available topics: {", ".join(t["id"] for t in metadata["topics"])}'

        parts: list[str] = [f"# {topic['name']}\n\n"]
        parts.append(f"**Topic ID:** {topic['id']}\n")
        parts.append(f"**URL:** [{topic['url']}]({topic['url']})\n\n")

        if include_videos:
            try:
                topic_name = topic["name"]
                videos_by_topic = await get_videos_by_topic(topic_name)

                videos_to_show: list[WWDCVideo] = []
                for v in videos_by_topic:
                    if year and year != "all" and v["year"] != year:
                        continue
                    videos_to_show.append(v)

                videos_to_show = videos_to_show[:limit]

                parts.append(f"## Videos ({len(videos_to_show)})\n\n")

                if not videos_to_show:
                    parts.append("No videos found for this topic.\n")
                else:
                    # Group by year
                    videos_by_year: dict[str, list[WWDCVideo]] = {}  # Changed type to WWDCVideo
                    for video in videos_to_show:
                        y = video["year"]
                        if y not in videos_by_year:
                            videos_by_year[y] = []
                        videos_by_year[y].append(video)

                    for y in sorted(videos_by_year.keys(), key=lambda x: int(x), reverse=True):
                        parts.append(f"### WWDC{y}\n\n")
                        for video in videos_by_year[y]:
                            parts.append(f"- [{video['title']}]({video['url']})")

                            features: list[str] = []
                            if video.get("hasTranscript"):
                                features.append("Transcript")
                            if video.get("hasCode"):
                                features.append("Code")

                            if features:
                                parts.append(f" | {' | '.join(features)}")
                            parts.append("\n")
                        parts.append("\n")

            except Exception as e:
                parts.append(f"Error loading topic videos: {str(e)}\n")

        return "".join(parts)

    except Exception as e:
        return f"Error: Failed to browse WWDC topics: {str(e)}"


def search_in_transcript(full_text: str, pattern: re.Pattern[str]) -> list[dict[str, str]]:
    """
    Search for a query in a transcript.

    Args:
        full_text: The full transcript text.
        pattern: The compiled search pattern.

    Returns:
        List of matches with context.
    """
    matches: list[dict[str, str]] = []

    for match in pattern.finditer(full_text):
        start = match.start()
        # Find start of the line
        line_start = full_text.rfind("\n", 0, start) + 1

        # Find end of the line
        line_end = full_text.find("\n", start)
        if line_end == -1:
            line_end = len(full_text)

        # Get context lines (previous, current, next)
        # Find previous line start
        prev_line_start = full_text.rfind("\n", 0, line_start - 1) + 1 if line_start > 0 else 0

        # Find next line end
        next_line_end = full_text.find("\n", line_end + 1)
        if next_line_end == -1:
            next_line_end = len(full_text)

        context_text = full_text[prev_line_start:next_line_end].strip()

        # Clean up newlines for display
        context = " ... ".join(line.strip() for line in context_text.splitlines() if line.strip())

        matches.append({"context": context})

        # Optimization: If we have enough matches, stop?
        # But the current logic returns all matches in the text.
        # Let's limit per video if needed, but search_wwdc_content limits total matches *across* videos.
        # Actually, search_wwdc_content takes matches[:3].
        # So we should probably limit here too?
        # search_wwdc_content does: matches.append(... for m in transcript_matches).
        # Then it sorts results by len(matches).
        # So we need a count. But do we need *all* text contexts?
        # Probably not. Let's stick to returning all for now, or limit to reasonable number (e.g. 10) to save processing.
        if len(matches) >= 10:
            break

    return matches


def search_in_code(code_examples: list[CodeExample], pattern: re.Pattern[str], language: str | None = None) -> list[dict[str, Any]]:
    """
    Search for a query in code examples.

    Args:
        code_examples: List of code examples.
        pattern: The compiled search pattern.
        language: Optional language filter.

    Returns:
        List of matches with context.
    """
    matches: list[dict[str, Any]] = []
    target_language = language.lower() if language else None

    for example in code_examples:
        if target_language and example["language"].lower() != target_language:
            continue

        code = example["code"]
        for match in pattern.finditer(code):
            start = match.start()

            line_start = code.rfind("\n", 0, start) + 1
            line_end = code.find("\n", start)
            if line_end == -1:
                line_end = len(code)

            matched_line = code[line_start:line_end].strip()

            matches.append({
                "context": f"[{example['language']}] {example.get('title', '')}: {matched_line}",
                "timestamp": example.get("timestamp"),
            })

            # Limit matches per example?
            if len(matches) >= 10:
                break

        if len(matches) >= 10:
            break

    return matches


def format_video_list(videos: list[WWDCVideo], year: str | None, topic: str | None, has_code: bool | None) -> str:
    """
    Format a list of videos into a markdown string.

    Args:
        videos: List of videos.
        year: Year filter used.
        topic: Topic filter used.
        has_code: Has code filter used.

    Returns:
        Formatted markdown string.
    """
    if not videos:
        return "No WWDC videos found matching the criteria."

    parts: list[str] = ["# WWDC Video List\n\n"]

    filters: list[str] = []
    if year and year != "all":
        filters.append(f"Year: {year}")
    if topic:
        filters.append(f"Topic: {topic}")
    if has_code is not None:
        filters.append(f"Has Code: {'Yes' if has_code else 'No'}")

    if filters:
        parts.append(f"**Filter Conditions:** {', '.join(filters)}\n\n")

    parts.append(f"**Found {len(videos)} videos**\n\n")

    videos_by_year: dict[str, list[WWDCVideo]] = {}
    for video in videos:
        y = video["year"]
        if y not in videos_by_year:
            videos_by_year[y] = []
        videos_by_year[y].append(video)

    for y in sorted(videos_by_year.keys(), key=lambda x: int(x), reverse=True):
        parts.append(f"## WWDC{y}\n\n")
        for video in videos_by_year[y]:
            parts.append(f"### [{video['title']}]({video['url']})\n")

            metadata: list[str] = []
            if video.get("duration"):
                metadata.append(f"Duration: {video['duration']}")
            speakers = video.get("speakers")
            if speakers:
                metadata.append(f"Speakers: {', '.join(speakers)}")
            if video.get("hasTranscript"):
                metadata.append("Transcript")
            if video.get("hasCode"):
                metadata.append("Code Examples")

            if metadata:
                parts.append(f"*{' | '.join(metadata)}*\n")

            topics = video.get("topics")
            if topics:
                parts.append(f"**Topics:** {', '.join(topics)}\n")

            parts.append("\n")

    return "".join(parts)


def format_search_results(results: list[dict[str, Any]], query: str, search_in: str) -> str:
    """
    Format search results into a markdown string.

    Args:
        results: List of search results.
        query: Search query.
        search_in: Search scope.

    Returns:
        Formatted markdown string.
    """
    if not results:
        scope = "code" if search_in == "code" else "transcript" if search_in == "transcript" else "content"
        return f'No {scope} found containing "{query}".'

    parts: list[str] = ["# WWDC Content Search Results\n\n"]
    parts.append(f'**Search Query:** "{query}"\n')
    scope_str = "Code" if search_in == "code" else "Transcript" if search_in == "transcript" else "All Content"
    parts.append(f"**Search Scope:** {scope_str}\n")
    parts.append(f"**Found {len(results)} related videos**\n\n")

    for result in results:
        video = result["video"]
        parts.append(f"## [{video['title']}]({video['url']})\n")
        parts.append(f"*WWDC{video['year']} | {len(result['matches'])} matches*\n\n")

        for match in result["matches"]:
            parts.append(f"**{'Code' if match['type'] == 'code' else 'Transcript'}**")
            if match.get("timestamp"):
                parts.append(f" ({match['timestamp']})")
            parts.append("\n")
            parts.append(f"> {match['context']}\n\n")

    return "".join(parts)


def format_video_detail(video: WWDCVideo, include_transcript: bool, include_code: bool) -> str:
    """
    Format video details into a markdown string.

    Args:
        video: Video data.
        include_transcript: Whether to include transcript.
        include_code: Whether to include code examples.

    Returns:
        Formatted markdown string.
    """
    parts: list[str] = [f"# {video['title']}\n\n"]
    parts.append(f"**WWDC{video['year']}** | [Watch Video]({video['url']})\n\n")

    if video.get("duration"):
        parts.append(f"**Duration:** {video['duration']}\n")
    speakers = video.get("speakers")
    if speakers:
        parts.append(f"**Speakers:** {', '.join(speakers)}\n")
    topics = video.get("topics")
    if topics:
        parts.append(f"**Topics:** {', '.join(topics)}\n")

    resources = video.get("resources", {})
    if resources.get("hdVideo") or resources.get("sdVideo") or resources.get("resourceLinks"):
        parts.append("\n**Resources:**\n")
        if resources.get("hdVideo"):
            parts.append(f"- [HD Video]({resources['hdVideo']})\n")
        if resources.get("sdVideo"):
            parts.append(f"- [SD Video]({resources['sdVideo']})\n")
        resource_links = resources.get("resourceLinks")
        if resource_links:
            for link in resource_links:
                parts.append(f"- [{link['title']}]({link['url']})\n")

    chapters = video.get("chapters")
    if chapters:
        parts.append("\n## Chapters\n\n")
        for chapter in chapters:
            parts.append(f"- **{chapter['timestamp']}** {chapter['title']}\n")

    if include_transcript and video.get("transcript"):
        parts.append("\n## Transcript\n\n")
        transcript = video["transcript"]
        if transcript and transcript.get("segments"):
            for segment in transcript["segments"]:
                parts.append(f"**{segment['timestamp']}**\n")
                parts.append(f"{segment['text']}\n\n")
        elif transcript:
            parts.append(transcript.get("fullText", ""))

    code_examples = video.get("codeExamples")
    if include_code and code_examples:
        parts.append("\n## Code Examples\n\n")
        for i, example in enumerate(code_examples):
            title = example.get("title") or f"Code Example {i + 1}"
            parts.append(f"### {title}")
            if example.get("timestamp"):
                parts.append(f" ({example['timestamp']})")
            parts.append("\n\n")

            parts.append(f"```{example['language']}\n")
            parts.append(example["code"])
            parts.append("\n```\n\n")

            if example.get("context"):
                parts.append(f"*{example['context']}*\n\n")

    related_videos = video.get("relatedVideos")
    if related_videos:
        parts.append("\n## Related Videos\n\n")
        for related in related_videos:
            parts.append(f"- [{related['title']}]({related['url']}) (WWDC{related['year']})\n")

    return "".join(parts)


def format_code_examples(
    examples: list[dict[str, Any]], framework: str | None, topic: str | None, language: str | None
) -> str:
    """
    Format code examples into a markdown string.

    Args:
        examples: List of code examples.
        framework: Framework filter.
        topic: Topic filter.
        language: Language filter.

    Returns:
        Formatted markdown string.
    """
    if not examples:
        return "No code examples found matching the criteria."

    parts: list[str] = ["# WWDC Code Examples\n\n"]

    filters: list[str] = []
    if framework:
        filters.append(f"Framework: {framework}")
    if topic:
        filters.append(f"Topic: {topic}")
    if language:
        filters.append(f"Language: {language}")

    if filters:
        parts.append(f"**Filter Conditions:** {', '.join(filters)}\n\n")

    parts.append(f"**Found {len(examples)} code examples**\n\n")

    examples_by_language: dict[str, list[dict[str, Any]]] = {}
    for ex in examples:
        lang = ex["language"]
        if lang not in examples_by_language:
            examples_by_language[lang] = []
        examples_by_language[lang].append(ex)

    for lang in examples_by_language:
        parts.append(f"## {lang.capitalize()}\n\n")
        for example in examples_by_language[lang]:
            parts.append(f"### {example.get('title') or 'Code Example'}\n")
            parts.append(f"*From: [{example['videoTitle']}]({example['videoUrl']}) (WWDC{example['year']})*")

            if example.get("timestamp"):
                parts.append(f" *@ {example['timestamp']}*")
            parts.append("\n\n")

            parts.append(f"```{example['language']}\n")
            parts.append(example["code"])
            parts.append("\n```\n\n")

    return "".join(parts)
