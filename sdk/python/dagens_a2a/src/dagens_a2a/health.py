"""
Health Check Utilities

Provides utilities for implementing health checks in A2A agents.
"""

import asyncio
import time
from dataclasses import dataclass, field
from enum import Enum
from typing import Any, Callable, Dict, List, Optional


class HealthStatus(str, Enum):
    """Health status levels"""
    HEALTHY = "healthy"
    DEGRADED = "degraded"
    UNHEALTHY = "unhealthy"


@dataclass
class HealthCheckResult:
    """Result of a health check"""
    name: str
    status: HealthStatus
    message: str = ""
    duration_ms: float = 0
    details: Dict[str, Any] = field(default_factory=dict)


@dataclass
class AggregatedHealth:
    """Aggregated health from multiple checks"""
    status: HealthStatus
    checks: List[HealthCheckResult]
    timestamp: float = field(default_factory=time.time)

    def to_dict(self) -> Dict[str, Any]:
        """Convert to dictionary for JSON response"""
        return {
            "status": self.status.value,
            "timestamp": self.timestamp,
            "checks": [
                {
                    "name": c.name,
                    "status": c.status.value,
                    "message": c.message,
                    "duration_ms": c.duration_ms,
                    "details": c.details,
                }
                for c in self.checks
            ],
        }


class HealthChecker:
    """
    Manages multiple health checks for an agent.

    Example:
        checker = HealthChecker()
        checker.add_check("database", check_db_connection)
        checker.add_check("llm_api", check_openai_api)

        health = await checker.run_all()
        # Returns AggregatedHealth with overall status
    """

    def __init__(self):
        self._checks: Dict[str, Callable] = {}

    def add_check(self, name: str, check_fn: Callable) -> None:
        """
        Add a health check.

        Args:
            name: Name of the check
            check_fn: Async or sync callable that returns HealthCheckResult or bool
        """
        self._checks[name] = check_fn

    def remove_check(self, name: str) -> None:
        """Remove a health check"""
        self._checks.pop(name, None)

    async def run_check(self, name: str, check_fn: Callable) -> HealthCheckResult:
        """Run a single health check"""
        start = time.time()
        try:
            if asyncio.iscoroutinefunction(check_fn):
                result = await check_fn()
            else:
                result = check_fn()

            duration_ms = (time.time() - start) * 1000

            # Handle different return types
            if isinstance(result, HealthCheckResult):
                result.duration_ms = duration_ms
                return result
            elif isinstance(result, bool):
                return HealthCheckResult(
                    name=name,
                    status=HealthStatus.HEALTHY if result else HealthStatus.UNHEALTHY,
                    duration_ms=duration_ms,
                )
            elif isinstance(result, dict):
                return HealthCheckResult(
                    name=name,
                    status=HealthStatus(result.get("status", "healthy")),
                    message=result.get("message", ""),
                    duration_ms=duration_ms,
                    details=result.get("details", {}),
                )
            else:
                return HealthCheckResult(
                    name=name,
                    status=HealthStatus.HEALTHY,
                    duration_ms=duration_ms,
                )

        except Exception as e:
            duration_ms = (time.time() - start) * 1000
            return HealthCheckResult(
                name=name,
                status=HealthStatus.UNHEALTHY,
                message=str(e),
                duration_ms=duration_ms,
            )

    async def run_all(self) -> AggregatedHealth:
        """Run all health checks and aggregate results"""
        if not self._checks:
            return AggregatedHealth(
                status=HealthStatus.HEALTHY,
                checks=[],
            )

        # Run checks concurrently
        tasks = [
            self.run_check(name, check_fn)
            for name, check_fn in self._checks.items()
        ]
        results = await asyncio.gather(*tasks)

        # Determine overall status
        statuses = [r.status for r in results]
        if HealthStatus.UNHEALTHY in statuses:
            overall = HealthStatus.UNHEALTHY
        elif HealthStatus.DEGRADED in statuses:
            overall = HealthStatus.DEGRADED
        else:
            overall = HealthStatus.HEALTHY

        return AggregatedHealth(
            status=overall,
            checks=list(results),
        )


# Pre-built health check functions

async def check_memory_usage(threshold_percent: float = 90) -> HealthCheckResult:
    """Check if memory usage is below threshold"""
    try:
        import psutil
        memory = psutil.virtual_memory()
        status = HealthStatus.HEALTHY if memory.percent < threshold_percent else HealthStatus.DEGRADED
        return HealthCheckResult(
            name="memory",
            status=status,
            message=f"Memory usage: {memory.percent}%",
            details={"percent": memory.percent, "threshold": threshold_percent},
        )
    except ImportError:
        return HealthCheckResult(
            name="memory",
            status=HealthStatus.HEALTHY,
            message="psutil not available, skipping memory check",
        )


async def check_disk_usage(path: str = "/", threshold_percent: float = 90) -> HealthCheckResult:
    """Check if disk usage is below threshold"""
    try:
        import psutil
        disk = psutil.disk_usage(path)
        status = HealthStatus.HEALTHY if disk.percent < threshold_percent else HealthStatus.DEGRADED
        return HealthCheckResult(
            name="disk",
            status=status,
            message=f"Disk usage: {disk.percent}%",
            details={"percent": disk.percent, "path": path, "threshold": threshold_percent},
        )
    except ImportError:
        return HealthCheckResult(
            name="disk",
            status=HealthStatus.HEALTHY,
            message="psutil not available, skipping disk check",
        )


def create_http_check(url: str, timeout: float = 5.0) -> Callable:
    """Create a health check that verifies an HTTP endpoint is reachable"""
    async def check() -> HealthCheckResult:
        try:
            import httpx
            async with httpx.AsyncClient() as client:
                response = await client.get(url, timeout=timeout)
                status = HealthStatus.HEALTHY if response.status_code < 400 else HealthStatus.UNHEALTHY
                return HealthCheckResult(
                    name=f"http:{url}",
                    status=status,
                    message=f"HTTP {response.status_code}",
                    details={"url": url, "status_code": response.status_code},
                )
        except Exception as e:
            return HealthCheckResult(
                name=f"http:{url}",
                status=HealthStatus.UNHEALTHY,
                message=str(e),
                details={"url": url},
            )
    return check
