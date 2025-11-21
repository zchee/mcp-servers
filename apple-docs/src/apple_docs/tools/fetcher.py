from typing import Any

from apple_docs.utils import formatter
from apple_docs.utils.constants import ProcessingLimits
from apple_docs.utils.http_client import http_client
from apple_docs.utils.url_converter import convert_to_json_api_url


async def fetch_apple_doc_json(
    url: str,
    include_related_apis: bool = False,
    include_references: bool = False,
    include_similar_apis: bool = False,
    include_platform_analysis: bool = False,
    max_depth: int = 2,
) -> str:
    """
    Fetch and format Apple Developer Documentation from a URL.

    Args:
        url: The URL of the documentation page.
        include_related_apis: Whether to include related APIs.
        include_references: Whether to include references.
        include_similar_apis: Whether to include similar APIs.
        include_platform_analysis: Whether to include platform compatibility analysis.
        max_depth: Maximum depth for recursive fetching (for references).

    Returns:
        Formatted documentation content.

    Raises:
        ValueError: If the URL is invalid.
    """
    if "developer.apple.com" not in url:
        raise ValueError("URL must be from developer.apple.com")

    json_api_url = url if ".json" in url else convert_to_json_api_url(url)
    if not json_api_url:
        raise ValueError("Invalid Apple Developer Documentation URL")

    # TODO(zchee): Implement caching here if needed

    json_data = await http_client.get_json(json_api_url)

    # Handle references if primary content is missing
    if not json_data.get("primaryContentSections") and json_data.get("references") and max_depth > 0:
        references = json_data["references"]
        if references:
            first_ref_key = next(iter(references))
            first_ref = references[first_ref_key]
            if first_ref.get("url"):
                ref_path = first_ref["url"]
                if ref_path.startswith("/documentation/"):
                    ref_path = ref_path.replace("/documentation/", "", 1)
                elif ref_path.startswith("/"):
                    ref_path = ref_path[1:]

                ref_url = f"https://developer.apple.com/tutorials/data/documentation/{ref_path}.json"
                return await fetch_apple_doc_json(
                    ref_url,
                    include_related_apis,
                    include_references,
                    include_similar_apis,
                    include_platform_analysis,
                    max_depth - 1,
                )

    return format_json_documentation(
        json_data, url, include_related_apis, include_references, include_similar_apis, include_platform_analysis
    )


def format_json_documentation(
    json_data: dict[str, Any],
    original_url: str,
    include_related_apis: bool,
    include_references: bool,
    include_similar_apis: bool,
    include_platform_analysis: bool,
) -> str:
    """
    Format JSON documentation data into markdown.

    Args:
        json_data: The JSON data from Apple.
        original_url: The original URL of the documentation.
        include_related_apis: Whether to include related APIs.
        include_references: Whether to include references.
        include_similar_apis: Whether to include similar APIs.
        include_platform_analysis: Whether to include platform analysis.

    Returns:
        Formatted markdown string.
    """
    content = ""

    content += formatter.format_document_header(json_data)
    content += formatter.format_document_abstract(json_data)

    if formatter.is_specific_api_document(json_data):
        content += formatter.format_specific_api_content(json_data)
    else:
        content += formatter.format_api_collection_content(json_data)

    content += formatter.format_platform_availability(json_data)
    content += formatter.format_see_also_section(json_data)

    if include_related_apis:
        related_apis = extract_related_apis(json_data)
        if related_apis:
            content += formatter.format_related_apis_section(related_apis)

    if include_references:
        references = extract_references(json_data)
        if references:
            content += formatter.format_references_section(references)

    if include_similar_apis:
        similar_apis = extract_similar_apis(json_data)
        if similar_apis:
            content += formatter.format_similar_apis_section(similar_apis)

    if include_platform_analysis:
        analysis = analyze_platform_compatibility(json_data)
        if analysis:
            content += formatter.format_platform_analysis_section(analysis)

    content += f"---\n\n[View full documentation on Apple Developer]({original_url})"

    return content


def extract_related_apis(json_data: dict[str, Any]) -> list[dict[str, str]]:
    """
    Extract related APIs from the documentation data.

    Args:
        json_data: The JSON data.

    Returns:
        List of related APIs.
    """
    related_apis: list[dict[str, str]] = []

    relationships_sections = json_data.get("relationshipsSections", [])
    if relationships_sections:
        for section in relationships_sections:
            identifiers = section.get("identifiers", [])
            for identifier in identifiers[: ProcessingLimits.MAX_RELATED_APIS_PER_SECTION]:
                ref = json_data.get("references", {}).get(identifier)
                if ref:
                    url = ref.get("url", "#")
                    if url.startswith("/"):
                        url = f"https://developer.apple.com{url}"
                    related_apis.append({
                        "title": ref.get("title", "Unknown"),
                        "url": url,
                        "relationship": section.get("title", "Related"),
                    })

    see_also_sections = json_data.get("seeAlsoSections", [])
    if see_also_sections:
        for section in see_also_sections:
            identifiers = section.get("identifiers", [])
            for identifier in identifiers[: ProcessingLimits.MAX_RELATED_APIS_PER_SECTION]:
                ref = json_data.get("references", {}).get(identifier)
                if ref:
                    url = ref.get("url", "#")
                    if url.startswith("/"):
                        url = f"https://developer.apple.com{url}"
                    related_apis.append({
                        "title": ref.get("title", "Unknown"),
                        "url": url,
                        "relationship": f"See Also: {section.get('title', 'Related')}",
                    })

    return related_apis[: ProcessingLimits.MAX_DOC_FETCHER_RELATED_APIS]


def extract_references(json_data: dict[str, Any]) -> list[dict[str, str]]:
    """
    Extract references from the documentation data.

    Args:
        json_data: The JSON data.

    Returns:
        List of references.
    """
    references: list[dict[str, str]] = []

    refs = json_data.get("references", {})
    # Limit processing
    for _, ref in list(refs.items())[: ProcessingLimits.MAX_DOC_FETCHER_REFERENCES]:
        url = ref.get("url", "#")
        if url.startswith("/"):
            url = f"https://developer.apple.com{url}"

        abstract = ""
        if ref.get("abstract"):
            abstract = " ".join(item.get("text", "") for item in ref["abstract"]).strip()

        references.append({
            "title": ref.get("title", "Unknown"),
            "url": url,
            "type": ref.get("role", ref.get("kind", "unknown")),
            "abstract": abstract,
        })

    return references


def extract_similar_apis(json_data: dict[str, Any]) -> list[dict[str, str]]:
    """
    Extract similar APIs from the documentation data.

    Args:
        json_data: The JSON data.

    Returns:
        List of similar APIs.
    """
    similar_apis: list[dict[str, str]] = []

    topic_sections = json_data.get("topicSections", [])
    if topic_sections:
        for section in topic_sections:
            identifiers = section.get("identifiers", [])
            for identifier in identifiers[: ProcessingLimits.MAX_RELATED_APIS_PER_SECTION]:
                ref = json_data.get("references", {}).get(identifier)
                if ref:
                    url = ref.get("url", "#")
                    if url.startswith("/"):
                        url = f"https://developer.apple.com{url}"
                    similar_apis.append({
                        "title": ref.get("title", "Unknown"),
                        "url": url,
                        "category": section.get("title", "Related"),
                    })

    return similar_apis[: ProcessingLimits.MAX_DOC_FETCHER_SIMILAR_APIS]


def analyze_platform_compatibility(json_data: dict[str, Any]) -> dict[str, Any] | None:
    """
    Analyze platform compatibility from the documentation data.

    Args:
        json_data: The JSON data.

    Returns:
        Platform compatibility analysis object.
    """
    platforms = json_data.get("metadata", {}).get("platforms")
    if not platforms:
        return None

    supported_platforms = ", ".join(p.get("name", "") for p in platforms)
    beta_platforms = [p.get("name") for p in platforms if p.get("beta") and p.get("name")]
    deprecated_platforms = [p.get("name") for p in platforms if p.get("deprecated") and p.get("name")]

    return {
        "supportedPlatforms": supported_platforms,
        "betaPlatforms": beta_platforms,
        "deprecatedPlatforms": deprecated_platforms,
        "crossPlatform": len(platforms) > 1,
        "platforms": platforms,
    }
