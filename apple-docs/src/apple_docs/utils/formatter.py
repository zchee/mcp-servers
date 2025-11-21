from typing import Any


def format_document_header(json_data: dict[str, Any]) -> str:
    """
    Format the document header (title, role, etc.).

    Args:
        json_data: The JSON data.

    Returns:
        Formatted header string.
    """
    metadata = json_data.get("metadata", {})
    title = metadata.get("title", "Untitled")
    role = metadata.get("role", "article")
    role_heading = metadata.get("roleHeading", "")

    header = f"# {title}\n"
    if role_heading:
        header += f"**{role_heading}**"
    if role:
        header += f" ({role})"
    header += "\n\n"

    return header


def format_document_abstract(json_data: dict[str, Any]) -> str:
    """
    Format the document abstract.

    Args:
        json_data: The JSON data.

    Returns:
        Formatted abstract string.
    """
    abstract_content = json_data.get("abstract", [])
    if not abstract_content:
        return ""

    text = " ".join(item.get("text", "") for item in abstract_content)
    return f"{text}\n\n"


def is_specific_api_document(json_data: dict[str, Any]) -> bool:
    """
    Check if the document is for a specific API (symbol).

    Args:
        json_data: The JSON data.

    Returns:
        True if it is a specific API document, False otherwise.
    """
    metadata = json_data.get("metadata", {})
    role = metadata.get("role", "")
    return role in ["symbol", "collectionGroup"]


def format_specific_api_content(json_data: dict[str, Any]) -> str:

    """


    Format content for a specific API document.





    Args:


        json_data: The JSON data.





    Returns:


        Formatted content string.


    """

    parts: list[str] = []

    primary_content = json_data.get("primaryContentSections", [])

    for section in primary_content:

        kind = section.get("kind")

        if kind == "declarations":

            parts.append("## Declaration\n\n")

            declarations = section.get("declarations", [])

            for decl in declarations:

                tokens = decl.get("tokens", [])

                code = "".join(token.get("text", "") for token in tokens)

                languages = decl.get("languages", ["swift"])

                for lang in languages:

                    parts.append(f"```{lang}\n{code}\n```\n\n")

        elif kind == "parameters":

            parts.append("## Parameters\n\n")

            params = section.get("parameters", [])

            for param in params:

                name = param.get("name", "")

                parts.append(f"- `{name}`: ")

                parts.append(" ".join(item.get("text", "") for item in param.get("content", [])))

                parts.append("\n")

            parts.append("\n")

        elif kind == "content":

            parts.append(format_content_section(section))

    return "".join(parts)


def format_api_collection_content(json_data: dict[str, Any]) -> str:

    """


    Format content for an API collection document.





    Args:


        json_data: The JSON data.





    Returns:


        Formatted content string.


    """

    parts: list[str] = []

    topic_sections = json_data.get("topicSections", [])

    for section in topic_sections:

        title = section.get("title")

        if title:

            parts.append(f"## {title}\n\n")

        identifiers = section.get("identifiers", [])

        for identifier in identifiers:

            ref = json_data.get("references", {}).get(identifier)

            if ref:

                name = ref.get("title", "Unknown")

                url = ref.get("url", "")

                if url.startswith("/"):

                    url = f"https://developer.apple.com{url}"

                parts.append(f"- [{name}]({url})")

                abstract = ref.get("abstract", [])

                if abstract:

                    text = " ".join(item.get("text", "") for item in abstract)

                    parts.append(f": {text}")

                parts.append("\n")

        parts.append("\n")

    return "".join(parts)


def format_content_section(section: dict[str, Any]) -> str:

    """


    Format a generic content section.





    Args:


        section: The section data.





    Returns:


        Formatted section string.


    """

    parts: list[str] = []

    text_content = section.get("content", [])

    for item in text_content:

        if item.get("type") == "paragraph":

            text = "".join(inline.get("text", "") for inline in item.get("inlineContent", []))

            parts.append(f"{text}\n\n")

        elif item.get("type") == "heading":

            level = item.get("level", 2)

            text = item.get("text", "")

            parts.append(f"{'#' * level} {text}\n\n")

        elif item.get("type") == "unorderedList":

            for list_item in item.get("items", []):

                text = "".join(


                    inline.get("text", "")


                    for para in list_item.get("content", [])


                    for inline in para.get("inlineContent", [])


                )

                parts.append(f"- {text}\n")

            parts.append("\n")

    return "".join(parts)


def format_platform_availability(json_data: dict[str, Any]) -> str:

    """


    Format platform availability information.





    Args:


        json_data: The JSON data.





    Returns:


        Formatted availability string.


    """

    metadata = json_data.get("metadata", {})

    platforms = metadata.get("platforms", [])

    if not platforms:

        return ""

    parts: list[str] = ["## Availability\n\n"]

    for platform in platforms:

        name = platform.get("name", "")

        introduced = platform.get("introducedAt", "")

        deprecated = platform.get("deprecatedAt", "")

        parts.append(f"- **{name}**: ")

        if introduced:

            parts.append(f"Introduced in {introduced}")

        if deprecated:

            parts.append(f", Deprecated in {deprecated}")

        parts.append("\n")

    parts.append("\n")

    return "".join(parts)


def format_see_also_section(json_data: dict[str, Any]) -> str:

    """


    Format "See Also" section.





    Args:


        json_data: The JSON data.





    Returns:


        Formatted section string.


    """

    see_also = json_data.get("seeAlsoSections", [])

    if not see_also:

        return ""

    parts: list[str] = ["## See Also\n\n"]

    for section in see_also:

        title = section.get("title")

        if title:

            parts.append(f"### {title}\n\n")

        identifiers = section.get("identifiers", [])

        for identifier in identifiers:

            ref = json_data.get("references", {}).get(identifier)

            if ref:

                name = ref.get("title", "Unknown")

                url = ref.get("url", "")

                if url.startswith("/"):

                    url = f"https://developer.apple.com{url}"

                parts.append(f"- [{name}]({url})\n")

        parts.append("\n")

    return "".join(parts)


def format_related_apis_section(related_apis: list[dict[str, str]]) -> str:

    """


    Format related APIs section.





    Args:


        related_apis: List of related API dictionaries.





    Returns:


        Formatted section string.


    """

    if not related_apis:

        return ""

    parts: list[str] = ["## Related APIs\n\n"]

    for api in related_apis:

        parts.append(f"- [{api['title']}]({api['url']}) ({api['relationship']})\n")

    parts.append("\n")

    return "".join(parts)


def format_references_section(references: list[dict[str, str]]) -> str:

    """


    Format references section.





    Args:


        references: List of reference dictionaries.





    Returns:


        Formatted section string.


    """

    if not references:

        return ""

    parts: list[str] = ["## References\n\n"]

    for ref in references:

        parts.append(f"- [{ref['title']}]({ref['url']}) ({ref['type']})\n")

        if ref.get("abstract"):

            parts.append(f"  {ref['abstract']}\n")

    parts.append("\n")

    return "".join(parts)


def format_similar_apis_section(similar_apis: list[dict[str, str]]) -> str:

    """


    Format similar APIs section.





    Args:


        similar_apis: List of similar API dictionaries.





    Returns:


        Formatted section string.


    """

    if not similar_apis:

        return ""

    parts: list[str] = ["## Similar APIs\n\n"]

    for api in similar_apis:

        parts.append(f"- [{api['title']}]({api['url']}) ({api['category']})\n")

    parts.append("\n")

    return "".join(parts)


def format_platform_analysis_section(analysis: dict[str, Any]) -> str:

    """


    Format platform analysis section.





    Args:


        analysis: Platform analysis dictionary.





    Returns:


        Formatted section string.


    """

    parts: list[str] = ["## Platform Analysis\n\n"]

    parts.append(f"**Supported Platforms:** {analysis['supportedPlatforms']}\n\n")

    if analysis["betaPlatforms"]:

        parts.append(f"**Beta Platforms:** {', '.join(analysis['betaPlatforms'])}\n\n")

    if analysis["deprecatedPlatforms"]:

        parts.append(f"**Deprecated Platforms:** {', '.join(analysis['deprecatedPlatforms'])}\n\n")

    parts.append(f"**Cross-Platform:** {'Yes' if analysis['crossPlatform'] else 'No'}\n\n")

    return "".join(parts)
