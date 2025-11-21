from fastmcp import FastMCP

from .tools.fetcher import fetch_apple_doc_json
from .tools.search import search_apple_docs
from .tools.technologies import list_technologies
from .utils.constants import ApiLimits


mcp = FastMCP("apple-docs")


@mcp.tool()
async def search(query: str, type: str = "all") -> str:
    """
    Search Apple Developer Documentation for APIs, frameworks, guides, and samples.
    Best for finding specific APIs, classes, or methods.

    Args:
        query: Search query for Apple Developer Documentation. Tips: Use specific API names (e.g., "UIViewController"), framework names (e.g., "SwiftUI"), or technical terms.
        type: Type of content to filter. Use "all" for comprehensive results, "documentation" for API references/guides, "sample" for code snippets. Default: "all".
    """
    return await search_apple_docs(query, type)


@mcp.tool()
async def get_content(
    url: str,
    include_related_apis: bool = False,
    include_references: bool = False,
    include_similar_apis: bool = False,
    include_platform_analysis: bool = False,
) -> str:
    """
    Get detailed content from a specific Apple Developer Documentation page.
    Use this after search to get full documentation.

    Args:
        url: Full URL of the Apple Developer Documentation page. Must start with https://developer.apple.com/documentation/.
        include_related_apis: Include inheritance hierarchy and protocol conformances. Default: false
        include_references: Resolve and include all referenced types and APIs. Default: false
        include_similar_apis: Discover APIs with similar functionality. Default: false
        include_platform_analysis: Analyze platform availability and version requirements. Default: false
    """
    return await fetch_apple_doc_json(
        url, include_related_apis, include_references, include_similar_apis, include_platform_analysis
    )


@mcp.tool()
async def list_tech(
    category: str | None = None,
    language: str | None = None,
    include_beta: bool = True,
    limit: int = ApiLimits.DEFAULT_TECHNOLOGIES_LIMIT,
) -> str:
    """
    Browse all Apple technologies and frameworks by category.
    Essential for discovering available frameworks and understanding Apple's technology ecosystem.

    Args:
        category: Filter by category (case-sensitive). Popular: "App frameworks", "Graphics and games", "App services", "Media", "System". Leave empty to see all.
        language: Filter by language support. "swift" for Swift-compatible frameworks, "occ" for Objective-C. Leave empty for all.
        include_beta: Include beta/preview technologies. Default: true
        limit: Max results per category. Default: 200
    """
    return await list_technologies(category, language, include_beta, limit)


@mcp.tool()
async def search_framework(
    framework: str,
    symbol_type: str = "all",
    name_pattern: str | None = None,
    language: str = "swift",
    limit: int = ApiLimits.DEFAULT_FRAMEWORK_SYMBOLS_LIMIT,
) -> str:
    """
    Browse and search symbols within a specific Apple framework.
    Perfect for exploring framework APIs, finding all views/controllers/delegates in a framework, or discovering available types.

    Args:
        framework: Framework identifier in lowercase. Common: "uikit", "swiftui", "foundation", "combine", "coredata".
        symbol_type: Filter by symbol type. Use "class" for UIViewController subclasses, "protocol" for delegates, "struct" for value types. Default: "all".
        name_pattern: Filter by name pattern. Use "*View" for all views, "UI*" for UI-prefixed symbols, "*Delegate" for delegates. Case-sensitive.
        language: Language preference. Default: "swift"
        limit: Results limit. Default: 50
    """
    from .tools.frameworks import search_framework_symbols

    return await search_framework_symbols(framework, symbol_type, name_pattern, language, limit)


@mcp.tool()
async def list_wwdc_videos(
    year: str | None = None, topic: str | None = None, has_code: bool | None = None, limit: int = 50
) -> str:
    """
    List WWDC videos with filtering options.

    Args:
        year: Filter by year (e.g., "2024", "2023"). Use "all" for all years.
        topic: Filter by topic name or ID.
        has_code: Filter videos that have code examples.
        limit: Max results. Default: 50
    """
    from .tools.wwdc import list_wwdc_videos

    return await list_wwdc_videos(year, topic, has_code, limit)


@mcp.tool()
async def search_wwdc_content(
    query: str, search_in: str = "both", year: str | None = None, language: str | None = None, limit: int = 20
) -> str:
    """
    Search within WWDC video transcripts and code examples.

    Args:
        query: Search query.
        search_in: Scope of search: "transcript", "code", or "both". Default: "both"
        year: Filter by year.
        language: Filter code examples by language (e.g., "swift", "objc").
        limit: Max results. Default: 20
    """
    from .tools.wwdc import search_wwdc_content

    return await search_wwdc_content(query, search_in, year, language, limit)


@mcp.tool()
async def get_wwdc_video(year: str, video_id: str, include_transcript: bool = True, include_code: bool = True) -> str:
    """
    Get detailed information for a specific WWDC video.

    Args:
        year: The year of the video (e.g., "2024").
        video_id: The ID of the video (e.g., "10123").
        include_transcript: Include full transcript. Default: true
        include_code: Include code examples. Default: true
    """
    from .tools.wwdc import get_wwdc_video

    return await get_wwdc_video(year, video_id, include_transcript, include_code)


@mcp.tool()
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
        limit: Max results. Default: 30
    """
    from .tools.wwdc import get_wwdc_code_examples

    return await get_wwdc_code_examples(framework, topic, year, language, limit)


@mcp.tool()
async def browse_wwdc_topics(
    topic_id: str | None = None, include_videos: bool = True, year: str | None = None, limit: int = 20
) -> str:
    """
    Browse WWDC topics and their associated videos.

    Args:
        topic_id: Specific topic ID to view. Leave empty to list all topics.
        include_videos: Include videos in the topic details. Default: true
        year: Filter videos by year.
        limit: Max videos to show per topic. Default: 20
    """
    from .tools.wwdc import browse_wwdc_topics

    return await browse_wwdc_topics(topic_id, include_videos, year, limit)


def main() -> None:
    """
    Main entry point for the Apple Developer Documentation MCP server.
    """
    mcp.run()
