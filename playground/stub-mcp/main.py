from mcp.server.fastmcp import FastMCP

mcp = FastMCP("stub-mcp", host="0.0.0.0", port=8090)


@mcp.tool()
def get_pod_logs(pod_name: str, namespace: str = "default", tail: int = 50) -> str:
    """Return fake pod logs simulating a DB timeout error."""
    lines = [
        f'[ERROR] pod={pod_name} ns={namespace} db_timeout after 3 retries',
        '[WARN]  connection pool exhausted: 0/10 connections available',
        '[ERROR] upstream payment-svc returned 503, circuit breaker OPEN',
        '[INFO]  health check: FAIL latency=5200ms threshold=2000ms',
        '[ERROR] db_timeout: query waited 30s for available connection',
    ]
    return "\n".join(lines[:tail])


@mcp.tool()
def list_nodes() -> str:
    """Return a fake two-node cluster listing."""
    return (
        "NAME            STATUS   ROLES           AGE   VERSION\n"
        "playground-cp   Ready    control-plane   10d   v1.32.0\n"
        "playground-w1   Ready    <none>          10d   v1.32.0"
    )


@mcp.tool()
def query_metrics(query: str) -> str:
    """Return a fake Prometheus instant query result."""
    return (
        f'{{"status":"success","data":{{'
        f'"resultType":"vector","result":['
        f'{{"metric":{{"__name__":"{query}"}},"value":[1700000000,"0.42"]}}'
        f']}}}}'
    )


if __name__ == "__main__":
    mcp.run(transport="sse")
