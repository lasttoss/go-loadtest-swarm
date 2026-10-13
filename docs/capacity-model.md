# Sizing a game backend: measure it, and measure two profiles

Capacity numbers for game backends are usually quoted as a single coefficient — "half a core per
thousand players" — and that number is wrong by a factor of three depending on one thing nobody
writes down: **how chatty the client is**.

## The two profiles

Same server, same build, same machine, two load profiles. The only real difference is messages per
bot per second: **0.75** in the quiet run and **3.7** in the busy one.

| Component | quiet profile (0.75 ops/bot/s) | busy profile (3.7 ops/bot/s) |
|---|---|---|
| Application server | 0.28 – 0.33 core / 1k players | **0.92** core / 1k players |
| Realtime UDP tier | 0.14 | **0.66** |
| Cluster ingress | 0.23 | **0.74** |
| **Whole node** | 0.89 (≈1,120 players/core) | **2.73** (≈365 players/core) |

Two conclusions that a single number hides:

1. **The realtime tier and the ingress are not free.** At the busy profile the UDP tier costs 0.66
   core per thousand players — second only to the application — and it hits a 500m limit at roughly
   **750 players**. A limit that fits the quiet profile silently caps the busy one.
2. **Ingress must not share a node with the game at scale.** 0.23 → 0.74 core per thousand players
   is CPU taken from the match loop, and it is the one component whose cost you cannot see inside
   your own pod graph.

## Memory is almost never the constraint

249–280 MiB resident at ~1,000 players, 33 MiB idle ⇒ **0.2–0.25 MB per player**. A 16 GiB node is
not the thing that binds; buying RAM is the wrong reflex. (Caveat: measured with synthetic players.
Real players upload custom artwork in some games, and that is a different measurement.)

## Limits are promises about burst

Before the limit change, `cpu.stat` on the application container showed **1,314 throttled periods,
about 70 s of cumulative throttling** in a single run — the container was capped while the node was
at 72% utilisation. After raising the limit: `nr_throttled = 0`.

```bash
# the check that turns "it feels slow" into a number
kubectl top pod -n <ns>
kubectl exec <pod> -c <container> -- cat /sys/fs/cgroup/cpu.stat   # nr_periods, nr_throttled, throttled_usec
```

## What to record on every run

`connected_clients` on the cache, `kubectl top pod` per container, `nr_throttled`, p50/p99 for auth
and join, business-error counts separate from transport errors, and messages per second. Then state
the profile alongside the number — a capacity figure without its traffic profile is a guess.
