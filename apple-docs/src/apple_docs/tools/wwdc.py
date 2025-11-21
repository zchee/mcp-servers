import asyncio
from collections.abc import Awaitable
import re
from typing import Any

from apple_docs.types.wwdc import CodeExample, TopicInfo, WWDCVideo
from apple_docs.utils.wwdc_data_source import (
    load_all_videos,
    load_global_metadata,
    load_video_data,
)


async def load_videos_data(video_files: list[str]) -> list[WWDCVideo]:
    """
    Load video data for a list of video files.

    Args:
        video_files: List of file paths (e.g., "videos/2024-10015.json").

    Returns:
        List of WWDCVideo objects.
    """
    tasks: list[Awaitable[WWDCVideo]] = []
    for file_path in video_files:
        # Extract year and video ID from filename (e.g., "videos/2024-10015.json")
        match = re.search(r"(\d{4})-(\d+)\.json$", file_path)
        if match:
            year, video_id = match.groups()
            tasks.append(load_video_data(year, video_id))

    results = await asyncio.gather(*tasks, return_exceptions=True)
    videos: list[WWDCVideo] = []
    for res in results:
        if isinstance(res, dict):
            videos.append(res)
    return videos


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
        # Use cached all videos list for better performance
        all_videos = await load_all_videos()

        # Apply filters
        filtered_videos = all_videos

        if year and year != "all":
            filtered_videos = [v for v in filtered_videos if v["year"] == year]

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

        # Pre-filter using cached all videos list
        all_videos = await load_all_videos()
        potential_videos: list[WWDCVideo] = []  # Using WWDCVideo type as it's what load_all_videos returns

        for v in all_videos:
            if year and v["year"] != year:
                continue

            title_match = query_lower in v.get("title", "").lower()
            topic_match = any(query_lower in t.lower() for t in v.get("topics", []))

            has_code = v.get("hasCode", False)
            has_transcript = v.get("hasTranscript", False)

            if (
                title_match
                or topic_match
                or (search_in in ["code", "both"] and has_code)
                or (search_in in ["transcript", "both"] and has_transcript)
            ):
                potential_videos.append(v)

        if not potential_videos:
            return format_search_results([], query, search_in)

        # Load full data for potential matches to search content
        # Use dataFile property if available, or construct path
        video_files: list[str] = []
        for v in potential_videos:
            if "dataFile" in v:
                video_files.append(v["dataFile"])
            else:
                video_files.append(f"videos/{v['year']}-{v['id']}.json")

        videos = await load_videos_data(video_files)

        for video in videos:
            matches: list[dict[str, Any]] = []

            # Search transcript
            transcript = video.get("transcript")
            if search_in in ["transcript", "both"] and transcript:
                transcript_matches = search_in_transcript(transcript["fullText"], query_lower)
                for m in transcript_matches:
                    matches.append({
                        "type": "transcript",
                        "context": m["context"],
                        "timestamp": m.get("timestamp"),
                    })

            # Search code
            code_examples: list[CodeExample] | None = video.get("codeExamples")
            if search_in in ["code", "both"] and code_examples:
                code_matches = search_in_code(code_examples, query_lower, language)
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

        # Pre-filter using cached all videos list
        all_videos = await load_all_videos()
        potential_videos: list[WWDCVideo] = []

        topic_lower = topic.lower() if topic else None

        for v in all_videos:
            if year and v["year"] != year:
                continue

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
        video_files: list[str] = []
        for v in potential_videos:
            if "dataFile" in v:
                video_files.append(v["dataFile"])
            else:
                video_files.append(f"videos/{v['year']}-{v['id']}.json")

        videos = await load_videos_data(video_files)

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

        topic: TopicInfo | None = next((t for t in metadata["topics"] if t["id"] == topic_id), None)
        if not topic:
            return f'Topic "{topic_id}" not found. Available topics: {", ".join(t["id"] for t in metadata["topics"])}'

        content = f"# {topic['name']}\n\n"
        content += f"**Topic ID:** {topic['id']}\n"
        content += f"**URL:** [{topic['url']}]({topic['url']})\n\n"

        if include_videos:
            try:
                all_videos = await load_all_videos()
                topic_name = topic["name"]

                videos_to_show: list[WWDCVideo] = []
                for v in all_videos:
                    if year and year != "all" and v["year"] != year:
                        continue
                    if topic_name in v.get("topics", []):
                        videos_to_show.append(v)

                videos_to_show = videos_to_show[:limit]

                content += f"## Videos ({len(videos_to_show)})\n\n"

                if not videos_to_show:
                    content += "No videos found for this topic.\n"
                else:
                    # Group by year
                    videos_by_year: dict[str, list[WWDCVideo]] = {}  # Changed type to WWDCVideo
                    for video in videos_to_show:
                        y = video["year"]
                        if y not in videos_by_year:
                            videos_by_year[y] = []
                        videos_by_year[y].append(video)

                    for y in sorted(videos_by_year.keys(), key=lambda x: int(x), reverse=True):
                        content += f"### WWDC{y}\n\n"
                        for video in videos_by_year[y]:
                            content += f"- [{video['title']}]({video['url']})"

                            features: list[str] = []
                            if video.get("hasTranscript"):
                                features.append("Transcript")
                            if video.get("hasCode"):
                                features.append("Code")

                            if features:
                                content += f" | {' | '.join(features)}"
                            content += "\n"
                        content += "\n"

            except Exception as e:
                content += f"Error loading topic videos: {str(e)}\n"

        return content

    except Exception as e:
        return f"Error: Failed to browse WWDC topics: {str(e)}"


def search_in_transcript(full_text: str, query: str) -> list[dict[str, str]]:
    """
    Search for a query in a transcript.

    Args:
        full_text: The full transcript text.
        query: The search query.

    Returns:
        List of matches with context.
    """
    matches: list[dict[str, str]] = []
    lines = full_text.split("\n")

    for i, line in enumerate(lines):
        if query in line.lower():
            context_lines = [lines[i - 1] if i > 0 else "", line, lines[i + 1] if i < len(lines) - 1 else ""]
            context = " ... ".join(ln.strip() for ln in context_lines if ln.strip())
            matches.append({"context": context})

    return matches


def search_in_code(code_examples: list[CodeExample], query: str, language: str | None = None) -> list[dict[str, Any]]:
    """
    Search for a query in code examples.

    Args:
        code_examples: List of code examples.
        query: The search query.
        language: Optional language filter.

    Returns:
        List of matches with context.
    """
    matches: list[dict[str, Any]] = []

    for example in code_examples:
        if language and example["language"].lower() != language.lower():
            continue

        if query in example["code"].lower():
            lines = example["code"].split("\n")
            matching_lines = [line for line in lines if query in line.lower()]

            if matching_lines:
                matches.append({
                    "context": f"[{example['language']}] {example.get('title', '')}: {matching_lines[0].strip()}",
                    "timestamp": example.get("timestamp"),
                })

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

    content = "# WWDC Video List\n\n"

    filters: list[str] = []
    if year and year != "all":
        filters.append(f"Year: {year}")
    if topic:
        filters.append(f"Topic: {topic}")
    if has_code is not None:
        filters.append(f"Has Code: {'Yes' if has_code else 'No'}")

    if filters:
        content += f"**Filter Conditions:** {', '.join(filters)}\n\n"

    content += f"**Found {len(videos)} videos**\n\n"

    videos_by_year: dict[str, list[WWDCVideo]] = {}
    for video in videos:
        y = video["year"]
        if y not in videos_by_year:
            videos_by_year[y] = []
        videos_by_year[y].append(video)

    for y in sorted(videos_by_year.keys(), key=lambda x: int(x), reverse=True):
        content += f"## WWDC{y}\n\n"
        for video in videos_by_year[y]:
            content += f"### [{video['title']}]({video['url']})\n"

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
                content += f"*{' | '.join(metadata)}*\n"

            topics = video.get("topics")
            if topics:
                content += f"**Topics:** {', '.join(topics)}\n"

            content += "\n"

    return content


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

    content = "# WWDC Content Search Results\n\n"
    content += f'**Search Query:** "{query}"\n'
    scope_str = "Code" if search_in == "code" else "Transcript" if search_in == "transcript" else "All Content"
    content += f"**Search Scope:** {scope_str}\n"
    content += f"**Found {len(results)} related videos**\n\n"

    for result in results:
        video = result["video"]
        content += f"## [{video['title']}]({video['url']})\n"
        content += f"*WWDC{video['year']} | {len(result['matches'])} matches*\n\n"

        for match in result["matches"]:
            content += f"**{'Code' if match['type'] == 'code' else 'Transcript'}**"
            if match.get("timestamp"):
                content += f" ({match['timestamp']})"
            content += "\n"
            content += f"> {match['context']}\n\n"

    return content


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
    content = f"# {video['title']}\n\n"
    content += f"**WWDC{video['year']}** | [Watch Video]({video['url']})\n\n"

    if video.get("duration"):
        content += f"**Duration:** {video['duration']}\n"
    speakers = video.get("speakers")
    if speakers:
        content += f"**Speakers:** {', '.join(speakers)}\n"
    topics = video.get("topics")
    if topics:
        content += f"**Topics:** {', '.join(topics)}\n"

    resources = video.get("resources", {})
    if resources.get("hdVideo") or resources.get("sdVideo") or resources.get("resourceLinks"):
        content += "\n**Resources:**\n"
        if resources.get("hdVideo"):
            content += f"- [HD Video]({resources['hdVideo']})\n"
        if resources.get("sdVideo"):
            content += f"- [SD Video]({resources['sdVideo']})\n"
        resource_links = resources.get("resourceLinks")
        if resource_links:
            for link in resource_links:
                content += f"- [{link['title']}]({link['url']})\n"

    chapters = video.get("chapters")
    if chapters:
        content += "\n## Chapters\n\n"
        for chapter in chapters:
            content += f"- **{chapter['timestamp']}** {chapter['title']}\n"

    if include_transcript and video.get("transcript"):
        content += "\n## Transcript\n\n"
        transcript = video["transcript"]
        if transcript and transcript.get("segments"):
            for segment in transcript["segments"]:
                content += f"**{segment['timestamp']}**\n"
                content += f"{segment['text']}\n\n"
        elif transcript:
            content += transcript.get("fullText", "")

    code_examples = video.get("codeExamples")
    if include_code and code_examples:
        content += "\n## Code Examples\n\n"
        for i, example in enumerate(code_examples):
            title = example.get("title") or f"Code Example {i + 1}"
            content += f"### {title}"
            if example.get("timestamp"):
                content += f" ({example['timestamp']})"
            content += "\n\n"

            content += f"```{example['language']}\n"
            content += example["code"]
            content += "\n```\n\n"

            if example.get("context"):
                content += f"*{example['context']}*\n\n"

    related_videos = video.get("relatedVideos")
    if related_videos:
        content += "\n## Related Videos\n\n"
        for related in related_videos:
            content += f"- [{related['title']}]({related['url']}) (WWDC{related['year']})\n"

    return content


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

    content = "# WWDC Code Examples\n\n"

    filters: list[str] = []
    if framework:
        filters.append(f"Framework: {framework}")
    if topic:
        filters.append(f"Topic: {topic}")
    if language:
        filters.append(f"Language: {language}")

    if filters:
        content += f"**Filter Conditions:** {', '.join(filters)}\n\n"

    content += f"**Found {len(examples)} code examples**\n\n"

    examples_by_language: dict[str, list[dict[str, Any]]] = {}
    for ex in examples:
        lang = ex["language"]
        if lang not in examples_by_language:
            examples_by_language[lang] = []
        examples_by_language[lang].append(ex)

    for lang in examples_by_language:
        content += f"## {lang.capitalize()}\n\n"
        for example in examples_by_language[lang]:
            content += f"### {example.get('title') or 'Code Example'}\n"
            content += f"*From: [{example['videoTitle']}]({example['videoUrl']}) (WWDC{example['year']})*"

            if example.get("timestamp"):
                content += f" *@ {example['timestamp']}*"
            content += "\n\n"

            content += f"```{example['language']}\n"
            content += example["code"]
            content += "\n```\n\n"

    return content
