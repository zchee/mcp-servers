import asyncio
import contextlib
import random
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
                follow_redirects=True,
            )
        return cls._instance

    def _prune_cache(self, cache: dict[str, Any]) -> None:
        """Ensure cache size stays within limits."""
        if len(cache) >= self.MAX_CACHE_SIZE:
            with contextlib.suppress(StopIteration):
                cache.pop(next(iter(cache)))

    def _get_random_headers(self) -> dict[str, str]:
        """Get headers with a random User-Agent."""
        return {"User-Agent": random.choice(RequestConfig.USER_AGENTS)}

    async def _request_with_retry(self, url: str) -> httpx.Response:
        """Make a GET request with retry logic and exponential backoff."""
        last_exception = None

        for attempt in range(RequestConfig.RETRIES + 1):
            try:
                response = await self.client.get(url, headers=self._get_random_headers())
                response.raise_for_status()
                return response
            except httpx.HTTPStatusError as e:
                last_exception = e
                # Don't retry 404 or client errors (except 429)
                if e.response.status_code == 404:
                    raise Exception(ErrorMessages.NOT_FOUND) from e
                if 400 <= e.response.status_code < 500 and e.response.status_code != 429:
                    raise Exception(f"{ErrorMessages.FETCH_FAILED}: {e}") from e

                # Retry on 429 and 5xx
                if attempt < RequestConfig.RETRIES:
                    await asyncio.sleep(RequestConfig.RETRY_DELAY * (2 ** attempt))
                    continue
            except httpx.RequestError as e:
                last_exception = e
                # Retry on network errors
                if attempt < RequestConfig.RETRIES:
                    await asyncio.sleep(RequestConfig.RETRY_DELAY * (2 ** attempt))
                    continue

        # If we exhausted retries
        if isinstance(last_exception, httpx.HTTPStatusError):
            raise Exception(f"{ErrorMessages.FETCH_FAILED}: {last_exception}") from last_exception
        if isinstance(last_exception, httpx.RequestError):
            raise Exception(f"{ErrorMessages.NETWORK_ERROR}: {last_exception}") from last_exception
        raise Exception(ErrorMessages.FETCH_FAILED)

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

        response = await self._request_with_retry(url)
        text = response.text
        self._prune_cache(self._text_cache)
        self._text_cache[url] = text
        return text

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

        response = await self._request_with_retry(url)
        try:
            data = response.json()
            self._prune_cache(self._json_cache)
            self._json_cache[url] = data
            return data
        except Exception as e:
            raise Exception(f"{ErrorMessages.PARSE_FAILED}: {e}") from e

    def clear_cache(self) -> None:
        """Clear all internal caches."""
        self._text_cache.clear()
        self._json_cache.clear()

    async def close(self) -> None:
        """Close the HTTP client."""
        await self.client.aclose()


http_client = HttpClient()
