"""Async helpers for executing blocking SDK calls."""

from __future__ import annotations

import asyncio
from collections.abc import Callable
import functools
from typing import Any, TypeVar


T = TypeVar("T")


def _partial[T](func: Callable[..., T], *args: Any, **kwargs: Any) -> Callable[[], T]:
    return functools.partial(func, *args, **kwargs)


async def run_blocking[T](func: Callable[..., T], /, *args: Any, **kwargs: Any) -> T:
    """Execute a blocking callable on the default executor without stalling the loop."""
    loop = asyncio.get_running_loop()
    return await loop.run_in_executor(None, _partial(func, *args, **kwargs))
