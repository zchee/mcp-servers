import contextlib
import re
from typing import Any

from apple_docs.utils.constants import ApiLimits, AppleUrls, ProcessingLimits
from apple_docs.utils.http_client import http_client


# Cache for parsed framework symbols
_framework_symbols_cache: dict[str, list[FrameworkSymbol]] = {}
FRAMEWORK_CACHE_SIZE = 50


class FrameworkSymbol:
    """Represents a symbol within a framework."""

    def __init__(self, title: str, path: str, type: str, language: str):
        """
        Initialize a FrameworkSymbol.

        Args:
            title: The title of the symbol.
            path: The path to the symbol's documentation.
            type: The type of the symbol (e.g., "class", "struct").
            language: The programming language (e.g., "swift").
        """
        self.title = title
        self.path = path
        self.type = type
        self.language = language

    def to_dict(self) -> dict[str, str]:
        """Convert the symbol to a dictionary."""
        return {
            "title": self.title,
            "path": self.path,
            "type": self.type,
            "language": self.language,
        }


def _prune_framework_cache() -> None:
    """Ensure cache size stays within limits."""
    if len(_framework_symbols_cache) >= FRAMEWORK_CACHE_SIZE:
        with contextlib.suppress(StopIteration):
            _framework_symbols_cache.pop(next(iter(_framework_symbols_cache)))


async def _fetch_framework_data(framework: str) -> dict[str, Any]:
    """Fetch framework data, trying multiple URL patterns."""
    index_url = f"{AppleUrls.TUTORIALS_DATA}index/{framework.lower()}"
    try:
        return await http_client.get_json(index_url)
    except Exception:
        # Try with 'documentation' prefix if direct index fails
        index_url = f"{AppleUrls.TUTORIALS_DATA}documentation/{framework.lower()}"
        return await http_client.get_json(index_url)


async def search_framework_symbols(
    framework: str,
    symbol_type: str = "all",
    name_pattern: str | None = None,
    language: str = "swift",
    limit: int = ApiLimits.DEFAULT_FRAMEWORK_SYMBOLS_LIMIT,
) -> str:
    """
    Search for symbols within a framework.

    Args:
        framework: The framework name.
        symbol_type: The type of symbol to search for.
        name_pattern: Regex pattern to filter by name.
        language: Programming language.
        limit: Max results.

    Returns:
        Formatted string containing search results.
    """
    try:
        cache_key = f"{framework.lower()}-{language}"
        if cache_key in _framework_symbols_cache:
            all_symbols = _framework_symbols_cache[cache_key]
        else:
            data = await _fetch_framework_data(framework)
            all_symbols = parse_index_items(data.get("interfaceLanguages", {}).get(language, []), language)
            _prune_framework_cache()
            _framework_symbols_cache[cache_key] = all_symbols

        # Filter symbols
        target_type = symbol_type.lower()
        pattern_obj: re.Pattern[str] | str | None = None
        if name_pattern:
            try:
                pattern_obj = re.compile(name_pattern, re.IGNORECASE)
            except re.error:
                pattern_obj = name_pattern.lower()

        symbols = [s for s in all_symbols if matches_criteria(s, target_type, pattern_obj)]

        # Format results
        parts: list[str] = [f"# {framework} Framework Symbols\n\n"]
        parts.append(f"**Found:** {len(symbols)} symbols")
        if symbol_type != "all":
            parts.append(f" (Type: {symbol_type})")
        if name_pattern:
            parts.append(f" (Pattern: {name_pattern})")
        parts.append("\n\n")

        if not symbols:
            parts.append("No symbols found matching your criteria.\n")
            # Suggest collections if any
            collections = [s for s in all_symbols if s.type == "collection"]
            if collections:
                parts.append("\n## Try exploring these collections:\n\n")
                for col in collections[: ProcessingLimits.MAX_COLLECTIONS_TO_SHOW]:
                    parts.append(f"- [{col.title}](https://developer.apple.com{col.path})\n")
        else:
            type_str = symbol_type if symbol_type == "all" else pluralize_type(symbol_type).lower()
            parts.append(f"**Found:** {len(symbols)} {type_str}")
            if len(symbols) > limit:
                parts.append(f" (Showing top {limit})")
            parts.append("\n\n")

            # Group by type
            symbols_by_type: dict[str, list[FrameworkSymbol]] = {}
            for s in symbols[:limit]:
                if s.type not in symbols_by_type:
                    symbols_by_type[s.type] = []
                symbols_by_type[s.type].append(s)

            for type_name in sorted(symbols_by_type.keys()):
                type_symbols = symbols_by_type[type_name]
                parts.append(f"## {pluralize_type(type_name)}\n")
                for s in type_symbols:
                    url = f"https://developer.apple.com{s.path}"
                    parts.append(f"- [{s.title}]({url})\n")
                parts.append("\n")

        return "".join(parts)

    except Exception as e:
        return f"Error: Failed to search framework symbols: {str(e)}"


def parse_index_items(items_data: list[dict[str, Any]], language: str) -> list[FrameworkSymbol]:
    """
    Parse index items into FrameworkSymbol objects.

    Args:
        items_data: List of item data dictionaries.
        language: Programming language.

    Returns:
        List of FrameworkSymbol objects.
    """
    symbols: list[FrameworkSymbol] = []
    for item in items_data:
        if item.get("type") == "groupMarker":
            if item.get("children"):
                symbols.extend(parse_index_items(item["children"], language))
        else:
            symbols.append(
                FrameworkSymbol(
                    title=item.get("title", "Unknown"),
                    path=item.get("path", ""),
                    type=item.get("type", "unknown"),
                    language=language,
                )
            )
    return symbols


def matches_criteria(
    symbol: FrameworkSymbol, target_type: str, pattern_obj: re.Pattern[str] | str | None
) -> bool:
    """
    Check if a symbol matches the search criteria.

    Args:
        symbol: The symbol to check.
        target_type: The target type filter (lowercase).
        pattern_obj: The compiled regex pattern or string to match.

    Returns:
        True if the symbol matches, False otherwise.
    """
    if target_type != "all" and symbol.type.lower() != target_type:
        return False

    return not (pattern_obj and not matches_pattern(symbol.title, pattern_obj))


def matches_pattern(text: str, pattern_obj: re.Pattern[str] | str) -> bool:
    """
    Check if text matches a regex pattern or string.

    Args:
        text: The text to check.
        pattern_obj: The compiled regex pattern or string.

    Returns:
        True if matches, False otherwise.
    """
    if isinstance(pattern_obj, re.Pattern):
        return pattern_obj.search(text) is not None
    return text.lower().find(pattern_obj) != -1


def pluralize_type(type_name: str) -> str:
    """
    Pluralize a type name.

    Args:
        type_name: The type name.

    Returns:
        The pluralized type name.
    """
    if type_name.endswith("s"):
        return type_name
    if type_name.endswith("y"):
        return type_name[:-1] + "ies"
    return type_name + "s"
