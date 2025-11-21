import contextlib
from typing import Any

import httpx

from .constants import ErrorMessages, RequestConfig


class HttpClient:
    """HTTP client for making requests."""

    _instance: HttpClient | None = None
    client: httpx.AsyncClient
    _text_cache: dict[str, str] = {}
    _json_cache: dict[str, Any] = {}
    MAX_CACHE_SIZE = 100

    def __new__(cls) -> HttpClient:
        """Create a new instance of HttpClient."""
        if cls._instance is None:
            cls._instance = super().__new__(cls)
            cls._instance.client = httpx.AsyncClient(
                timeout=RequestConfig.TIMEOUT,
                headers={"User-Agent": RequestConfig.DEFAULT_SAFARI_USER_AGENT},
                follow_redirects=True,
            )
        return cls._instance

    def _prune_cache(self, cache: dict[str, Any]) -> None:
        """Ensure cache size stays within limits."""
        if len(cache) >= self.MAX_CACHE_SIZE:
            with contextlib.suppress(StopIteration):
                cache.pop(next(iter(cache)))

    async def get_text(self, url: str) -> str:
        """
        Get text content from a URL.

        Args:
            url: The URL to fetch.

        Returns:
            The text content of the response.

        Raises:
            Exception: If the request fails.
        """
        if url in self._text_cache:
            return self._text_cache[url]

        try:
            response = await self.client.get(url)
            response.raise_for_status()
            text = response.text
            self._prune_cache(self._text_cache)
            self._text_cache[url] = text
            return text
        except httpx.HTTPStatusError as e:
            if e.response.status_code == 404:
                raise Exception(ErrorMessages.NOT_FOUND) from e
            raise Exception(f"{ErrorMessages.FETCH_FAILED}: {e}") from e
        except httpx.RequestError as e:
            raise Exception(f"{ErrorMessages.NETWORK_ERROR}: {e}") from e
        except Exception as e:
            raise Exception(f"{ErrorMessages.FETCH_FAILED}: {e}") from e

    async def get_json(self, url: str) -> Any:
        """
        Get JSON content from a URL.

        Args:
            url: The URL to fetch.

        Returns:
            The JSON content of the response.

        Raises:
            Exception: If the request fails.
        """
        if url in self._json_cache:
            return self._json_cache[url]

        try:
            response = await self.client.get(url)
            response.raise_for_status()
            data = response.json()
            self._prune_cache(self._json_cache)
            self._json_cache[url] = data
            return data
        except httpx.HTTPStatusError as e:
            if e.response.status_code == 404:
                raise Exception(ErrorMessages.NOT_FOUND) from e
            raise Exception(f"{ErrorMessages.FETCH_FAILED}: {e}") from e
        except httpx.RequestError as e:
            raise Exception(f"{ErrorMessages.NETWORK_ERROR}: {e}") from e
        except Exception as e:
            raise Exception(f"{ErrorMessages.PARSE_FAILED}: {e}") from e


http_client = HttpClient()
