from typing import Any
from urllib.parse import quote

from bs4 import BeautifulSoup, Tag

from apple_docs.utils.constants import ApiLimits, AppleUrls
from apple_docs.utils.http_client import http_client


class SearchResult:
    """Represents a search result from Apple Developer Documentation."""

    def __init__(self, title: str, url: str, category: str, description: str = ""):
        """
        Initialize a SearchResult.

        Args:
            title: The title of the result.
            url: The URL of the result.
            category: The category of the result.
            description: The description of the result.
        """
        self.title = title
        self.url = url
        self.category = category
        self.description = description

    def to_dict(self) -> dict[str, Any]:
        """Convert the result to a dictionary."""
        return {
            "title": self.title,
            "url": self.url,
            "category": self.category,
            "description": self.description,
        }


async def search_apple_docs(query: str, type: str = "all") -> str:
    """
    Search Apple Developer Documentation.

    Args:
        query: The search query.
        type: The type of content to filter by.

    Returns:
        Formatted string containing search results.
    """
    search_url = f"{AppleUrls.SEARCH}?q={quote(query)}"

    try:
        html = await http_client.get_text(search_url)
    except Exception as e:
        return f"Error: Failed to perform search: {str(e)}"

    soup = BeautifulSoup(html, "lxml")
    results: list[SearchResult] = []
    filter_type = None if type == "all" else type

    search_results = soup.select(".search-result")
    for element in search_results:
        if len(results) >= ApiLimits.MAX_SEARCH_RESULTS:
            break

        result = parse_search_result(element, filter_type)
        if result:
            results.append(result)

    return format_search_results(results, query)


def parse_search_result(element: Tag, filter_type: str | None) -> SearchResult | None:
    """
    Parse a search result element.

    Args:
        element: The HTML element.
        filter_type: The type filter.

    Returns:
        SearchResult object or None.
    """
    link = element.select_one("a.search-result--title")
    if not link:
        return None

    href = link.get("href")
    if not href or not isinstance(href, str):
        return None

    url = f"https://developer.apple.com{href}" if href.startswith("/") else href
    title = link.get_text(strip=True)

    category_el = element.select_one(".search-result--category")
    category = category_el.get_text(strip=True) if category_el else "Unknown"

    description_el = element.select_one(".search-result--description")
    description = description_el.get_text(strip=True) if description_el else ""

    # Extract framework if available
    framework = extract_framework(element, url)
    if framework:
        category = f"{category} ({framework})"

    if filter_type and filter_type.lower() not in category.lower():
        return None

    return SearchResult(title, url, category, description)


def extract_framework(element: Tag, url: str) -> str | None:
    """
    Extract framework name from search result.

    Args:
        element: The HTML element.
        url: The URL of the result.

    Returns:
        Framework name or None.
    """
    # Try to extract from breadcrumbs or URL
    breadcrumbs = element.select(".search-result--breadcrumbs")
    if breadcrumbs:
        text = breadcrumbs[0].get_text(strip=True)
        parts = text.split(">")
        if len(parts) > 0:
            return parts[0].strip()

    # Fallback to URL parsing
    if "/documentation/" in url:
        parts = url.split("/documentation/")
        if len(parts) > 1:
            framework_part = parts[1].split("/")[0]
            return framework_part.capitalize()

    return None


def format_search_results(results: list[SearchResult], query: str) -> str:
    """
    Format search results into markdown.

    Args:
        results: List of SearchResult objects.
        query: The search query.

    Returns:
        Formatted markdown string.
    """
    if not results:
        # If no results found, suggest WWDC videos
        return (
            f'No documentation found for "{query}".\n\n'
            "Try searching WWDC videos using `search_wwdc_content` tool, "
            "or browse topics with `browse_wwdc_topics`."
        )

    parts: list[str] = [f'# Search Results for "{query}"\n\n']
    parts.append(f"Found {len(results)} results:\n\n")

    # Group by category
    grouped: dict[str, list[SearchResult]] = {}
    for result in results:
        cat = result.category
        if cat not in grouped:
            grouped[cat] = []
        grouped[cat].append(result)

    for category in sorted(grouped.keys()):
        parts.append(f"## {category}\n\n")
        for result in grouped[category]:
            parts.append(f"- [{result.title}]({result.url})\n")
            if result.description:
                parts.append(f"  {result.description}\n")
        parts.append("\n")

    return "".join(parts)
