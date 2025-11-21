from urllib.parse import urlparse


def convert_to_json_api_url(url: str) -> str | None:
    """
    Convert a standard Apple Developer URL to its JSON API equivalent.

    Args:
        url: The standard URL.

    Returns:
        The JSON API URL, or None if invalid.
    """
    if not is_valid_apple_developer_url(url):
        return None

    # If already a JSON URL, return as is
    if url.endswith(".json"):
        return url

    parsed = urlparse(url)
    path = parsed.path

    # Remove /documentation/ prefix if present
    if path.startswith("/documentation/"):
        path = path.replace("/documentation/", "", 1)
    elif path.startswith("/"):
        path = path[1:]

    # Construct the JSON API URL
    # The pattern seems to be: https://developer.apple.com/tutorials/data/documentation/{path}.json
    return f"https://developer.apple.com/tutorials/data/documentation/{path}.json"


def is_valid_apple_developer_url(url: str) -> bool:
    """
    Check if a URL is a valid Apple Developer URL.

    Args:
        url: The URL to check.

    Returns:
        True if valid, False otherwise.
    """
    try:
        parsed = urlparse(url)
        return parsed.netloc == "developer.apple.com"
    except Exception:
        return False


def extract_api_name_from_url(url: str) -> str | None:
    """
    Extract the API name from a URL.

    Args:
        url: The URL.

    Returns:
        The API name, or None if extraction fails.
    """
    try:
        parsed = urlparse(url)
        path = parsed.path
        parts = path.split("/")
        # Filter empty parts
        parts = [p for p in parts if p]

        if not parts:
            return None

        return parts[-1].replace(".json", "")
    except Exception:
        return None
