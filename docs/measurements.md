# Measurements

A load generator that reports its own numbers has to be able to fail, and the run that shows what it
reports has to be repeatable.

## Environment

| | |
|---|---|
| Machine | 12-core x86-64 (AMD64), Ubuntu 24.04, Docker 29.1.3 |
| Go | 1.27.2 |
| Date | 2026-10-13 (UTC) |

Every host port in this file is the one the repository publishes: bound to loopback and offset from
the default, so a machine already running a Postgres, a Redis or three other stacks does not have to
stop them first.


## 89.9% of statements

```
$ go test ./... -cover
ok  github.com/lasttoss/go-loadtest-swarm  1.426s  coverage: 89.9% of statements
```

**Result.** Exact. 89.9% is the root package, which is where the harness lives; `cmd/loadtest` and
`cmd/smoke-server` are wiring, so the module total is 71.1%.

## The smoke run: a report and an exit code

```
$ make smoke
## load test: local smoke

| requests | failures | rejected (4xx) | requests/s | clients | duration |
|---|---|---|---|---|---|
| 4852 | 4 | 0 | 1617.0 | 4 | 3.001s |

### latency (histogram, within 1%)

| p50 | p90 | p95 | p99 | p99.9 | max |
|---|---|---|---|---|---|
| 2ms | 3ms | 4ms | 6ms | 14ms | 58ms |

every assertion held: 4852 requests, p95 4ms, 1617.0 requests/s
```

**Result.** The absolute numbers move between machines and runs - the README's own run was
`5653 requests, p95 3ms, 1884.2 requests/s` - and that is the point of the two numbers the README
calls out: **p50 2 ms next to a max of 58 ms in the same run**. A mean would have reported something
in between and hidden both. The four failures are the scenario's own 1% error budget being spent by
the local socket, which is why they do not fail the build.
