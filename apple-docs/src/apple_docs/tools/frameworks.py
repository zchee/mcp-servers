import re
from typing import Any

from apple_docs.utils.constants import ApiLimits, AppleUrls, ProcessingLimits
from apple_docs.utils.http_client import http_client


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
        index_url = f"{AppleUrls.TUTORIALS_DATA}index/{framework.lower()}"

        # TODO(zchee): Implement caching

        try:
            data = await http_client.get_json(index_url)
        except Exception:
            # Try with 'documentation' prefix if direct index fails
            index_url = f"{AppleUrls.TUTORIALS_DATA}documentation/{framework.lower()}"
            data = await http_client.get_json(index_url)

        all_symbols = parse_index_items(data.get("interfaceLanguages", {}).get(language, []), language)

        # Filter symbols
        symbols = [s for s in all_symbols if matches_criteria(s, symbol_type, name_pattern)]

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
            index_items = data.get("interfaceLanguages", {}).get(language, [])
            collections = find_collections(index_items)
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


def matches_criteria(symbol: FrameworkSymbol, symbol_type: str, name_pattern: str | None) -> bool:
    """
    Check if a symbol matches the search criteria.

    Args:
        symbol: The symbol to check.
        symbol_type: The type filter.
        name_pattern: The name pattern filter.

    Returns:
        True if the symbol matches, False otherwise.
    """
    if symbol_type != "all" and symbol.type.lower() != symbol_type.lower():
        return False

    return not (name_pattern and not matches_pattern(symbol.title, name_pattern))


def matches_pattern(text: str, pattern: str) -> bool:
    """
    Check if text matches a regex pattern.

    Args:
        text: The text to check.
        pattern: The regex pattern.

    Returns:
        True if matches, False otherwise.
    """
    try:
        return re.search(pattern, text, re.IGNORECASE) is not None
    except re.error:
        return text.lower().find(pattern.lower()) != -1


def find_collections(items_data: list[dict[str, Any]]) -> list[FrameworkSymbol]:
    """
    Find collection items in the index data.

    Args:
        items_data: List of item data dictionaries.

    Returns:
        List of collection FrameworkSymbol objects.
    """
    collections: list[FrameworkSymbol] = []
    for item in items_data:
        if item.get("type") == "collection":
            collections.append(
                FrameworkSymbol(
                    title=item.get("title", "Unknown"),
                    path=item.get("path", ""),
                    type="collection",
                    language="swift",
                )
            )
        elif item.get("children"):
            collections.extend(find_collections(item["children"]))
    return collections


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
