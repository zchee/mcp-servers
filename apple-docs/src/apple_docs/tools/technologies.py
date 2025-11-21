from dataclasses import dataclass
from typing import Any

from apple_docs.utils.constants import ApiLimits, AppleUrls
from apple_docs.utils.http_client import http_client


@dataclass
class Technology:
    """Represents an Apple technology."""

    title: str
    url: str
    description: str
    tags: list[str]


@dataclass
class TechnologyGroup:
    """Represents a group of technologies."""

    title: str
    technologies: list[Technology]


async def list_technologies(
    category: str | None = None,
    language: str | None = None,
    include_beta: bool = True,
    limit: int = ApiLimits.DEFAULT_TECHNOLOGIES_LIMIT,
) -> str:
    """
    List available technologies.

    Args:
        category: Filter by category.
        language: Filter by language.
        include_beta: Include beta technologies.
        limit: Max results.

    Returns:
        Formatted string containing technologies.
    """
    try:
        data = await http_client.get_json(AppleUrls.TECHNOLOGIES_JSON)
        technologies = parse_technologies(data)

        filtered_technologies = apply_technology_filters(technologies, category, language, include_beta, limit)

        return format_technologies(filtered_technologies)

    except Exception as e:
        return f"Error: Failed to list technologies: {str(e)}"


def parse_technologies(data: dict[str, Any]) -> list[TechnologyGroup]:
    """
    Parse technologies JSON data.

    Args:
        data: The JSON data.

    Returns:
        List of TechnologyGroup objects.
    """
    groups: list[TechnologyGroup] = []
    sections = data.get("sections", [])

    for section in sections:
        group_title = section.get("title", "Unknown")
        techs: list[Technology] = []

        for item in section.get("items", []):
            techs.append(
                Technology(
                    title=item.get("title", "Unknown"),
                    url=f"https://developer.apple.com{item.get('path', '')}",
                    description=item.get("description", ""),
                    tags=item.get("tags", []),
                )
            )

        if techs:
            groups.append(TechnologyGroup(title=group_title, technologies=techs))

    return groups


def apply_technology_filters(
    groups: list[TechnologyGroup],
    category: str | None,
    language: str | None,
    include_beta: bool,
    limit: int,
) -> list[TechnologyGroup]:
    """
    Apply filters to technology groups.

    Args:
        groups: List of TechnologyGroup objects.
        category: Category filter.
        language: Language filter.
        include_beta: Include beta filter.
        limit: Max results limit.

    Returns:
        Filtered list of TechnologyGroup objects.
    """
    filtered_groups: list[TechnologyGroup] = []
    count = 0

    target_category = category.lower() if category else None
    target_language = language.lower() if language else None

    for group in groups:
        if target_category and target_category not in group.title.lower():
            continue

        filtered_techs: list[Technology] = []
        for tech in group.technologies:
            if count >= limit:
                break

            if not include_beta and "beta" in tech.tags:
                continue

            if target_language and (
                target_language not in [t.lower() for t in tech.tags]
                and target_language not in tech.description.lower()
            ):
                # This is a heuristic as language info might not be explicit in tags
                # But sometimes tags include 'swift', 'objc'
                continue

            filtered_techs.append(tech)
            count += 1

        if filtered_techs:
            filtered_groups.append(TechnologyGroup(title=group.title, technologies=filtered_techs))

        if count >= limit:
            break

    return filtered_groups


def format_technologies(groups: list[TechnologyGroup]) -> str:
    """
    Format technology groups into markdown.

    Args:
        groups: List of TechnologyGroup objects.

    Returns:
        Formatted markdown string.
    """
    if not groups:
        return "No technologies found matching your criteria."

    parts: list[str] = ["# Apple Technologies\n\n"]

    total_techs = sum(len(g.technologies) for g in groups)
    parts.append(f"Found {total_techs} technologies in {len(groups)} categories.\n\n")

    for group in groups:
        parts.append(f"## {group.title}\n\n")
        for tech in group.technologies:
            parts.append(f"- [{tech.title}]({tech.url})\n")
            if tech.description:
                parts.append(f"  {tech.description}\n")
            if tech.tags:
                parts.append(f"  *Tags: {', '.join(tech.tags)}*\n")
        parts.append("\n")

    return "".join(parts)
