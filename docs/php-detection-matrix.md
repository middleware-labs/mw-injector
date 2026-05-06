# PHP Detection & Instrumentation Matrix

Analysis of every PHP deployment pattern against three detection/instrumentation systems:

1. **mw-injector** (our process discovery via `/proc` scanning)
2. **OBI** (OpenTelemetry eBPF Instrumentation — network/eBPF-level tracing)
3. **OTel PHP Extension** (`opentelemetry.so` — PHP engine-level instrumentation via `zend_observer`)

---

## Detection Matrix

| # | Deployment Pattern | Exe Basename | Separate PHP Process? | mw-injector | OBI | OTel Extension |
|---|---|---|---|---|---|---|
| 1 | PHP-FPM workers (Nginx/Apache proxy_fcgi) | `php-fpm`, `php-fpm8.2` | YES | **DETECTED** | FastCGI parser — full support | Full support |
| 2 | PHP-FPM workers (Docker php:fpm) | `php-fpm` | YES | **DETECTED** | Full support | Full support |
| 3 | PHP-FPM (cPanel ea-php) | `php-fpm` | YES | **DETECTED** | Full support | Full support |
| 4 | PHP-FPM (Plesk) | `php-fpm` | YES | **DETECTED** | Full support | Full support |
| 5 | PHP-FPM (DirectAdmin) | `php-fpm` | YES | **DETECTED** | Full support | Full support |
| 6 | PHP-FPM (Remi repo RHEL) | `php-fpm` | YES | **DETECTED** | Full support | Full support |
| 7 | PHP-FPM (CloudLinux alt-php) | `php-fpm` | YES | **DETECTED** | Full support | Full support |
| 8 | PHP-CGI / spawn-fcgi | `php-cgi`, `php-cgi8.2` | YES | **DETECTED** | HTTP-level only | Full support |
| 9 | PHP CLI script | `php`, `php8.2` | YES | **DETECTED** | No coverage (no socket) | Full support |
| 10 | LiteSpeed Enterprise LSAPI | `lsphp`, `lsphp82` | YES | **DETECTED** | HTTP-level only | Full support |
| 11 | OpenLiteSpeed LSAPI | `lsphp`, `lsphp82` | YES | **DETECTED** | HTTP-level only | Full support |
| 12 | CloudLinux mod_lsapi + alt-php | `lsphp` | YES | **DETECTED** | HTTP-level only | Full support |
| 13 | cPanel + LiteSpeed ea-php | `lsphp` | YES | **DETECTED** | HTTP-level only | Full support |
| 14 | Apache mod_php (libphp.so) | `apache2` / `httpd` | **NO** | **DETECTED** (deep scan: `/proc/maps` for `libphp*.so`) | HTTP-level (sees Apache, not PHP) | Full support (via php.ini) |
| 15 | Docker php:apache image | `apache2` | **NO** | **DETECTED** (deep scan) | HTTP-level only | Full support |
| 16 | Laravel Octane + Swoole | `php` | YES | **DETECTED** (as php-cli) | HTTP-level only (generic TCP) | Works with `context-swoole` pkg |
| 17 | Swoole standalone | `php` | YES | **DETECTED** (as php-cli) | HTTP-level only | Works with `context-swoole` pkg |
| 18 | ReactPHP standalone | `php` | YES | **DETECTED** (as php-cli) | HTTP-level only | Full support |
| 19 | AMPHP standalone | `php` | YES | **DETECTED** (as php-cli) | HTTP-level only | Full support |
| 20 | Workerman / webman | `php` | YES | **DETECTED** (as php-cli) | HTTP-level only | Full support |
| 21 | FrankenPHP | `frankenphp` | YES (threads, not processes) | **DETECTED** (as frankenphp) | Go uprobes (Caddy HTTP, not PHP) | **Broken** in worker mode |
| 22 | **RoadRunner (Go server)** | `rr` | YES (Go binary) | **NOT DETECTED** (Go binary) | Go uprobes on `rr` | N/A (Go process) |
| 23 | RoadRunner (PHP workers) | `php` | YES | **DETECTED** (as php-cli) | No coverage (IPC, not HTTP) | Works with context care |
| 24 | Laravel artisan queue:work | `php` | YES | **DETECTED** (as php-cli) | No coverage (no HTTP socket) | Works (context leak risk) |
| 25 | Symfony messenger:consume | `php` | YES | **DETECTED** (as php-cli) | No coverage | Works |
| 26 | Drupal drush cron | `php` | YES | **DETECTED** (as php-cli) | No coverage | Works |
| 27 | Magento cron/indexer | `php` | YES | **DETECTED** (as php-cli) | No coverage | Works |
| 28 | WordPress wp-cli | `php` | YES | **DETECTED** (as php-cli) | No coverage | Works |
| 29 | PHP built-in dev server | `php` | YES | **DETECTED** (as php-cli) | HTTP-level only | Full support |
| 30 | cPanel suPHP (legacy) | `suphp` → short-lived `php` | YES (transient) | php child detected if alive long enough | No practical coverage | Full support |
| 31 | **Static PHP binary** | custom name | YES | **NOT DETECTED** | Maybe HTTP-level | Maybe (if PHP 8.0+) |
| 32 | Bref (AWS Lambda) | `php` / `php-fpm` | YES (no host /proc) | **N/A** (Lambda) | N/A | Works |
| 33 | PeachPie (.NET CLR) | `dotnet` | YES (no PHP exe) | **NOT DETECTED** | No PHP coverage | N/A |

---

## Gaps in mw-injector Detection

| Gap | Patterns Affected | Real-World Impact | Fix Difficulty | Notes |
|---|---|---|---|---|
| ~~**mod_php**~~ | ~~#14, #15~~ | ~~Moderate~~ | ~~Medium~~ | **FIXED** — deep scan via `/proc/<pid>/maps` for `libphp*.so` |
| ~~**FrankenPHP**~~ | ~~#21~~ | ~~Low–Moderate~~ | ~~Easy~~ | **FIXED** — `frankenphp` added to detection regex |
| **RoadRunner server** | #22 | Low — PHP workers (#23) are already detected | Not needed — the PHP workers are caught | `rr` is a Go binary |
| **Static PHP binary** | #31 | Very low — rare in production servers | Hard — requires ELF/maps inspection | Same problem as Rust compiled binaries |
| **PeachPie** | #33 | Negligible — experimental | Impractical — PHP compiled to .NET IL | Not worth supporting |

---

## OBI Instrumentation Details

### How OBI Works (Architecture)

OBI uses two complementary eBPF mechanisms:

1. **Language-level uprobes**: Attaches to specific user-space functions in the runtime (Go `net/http`, Java servlets, etc.). Rich semantic spans with HTTP method, URL, status, latency.

2. **Network/kernel-level kprobes**: Hooks `sys_accept`, `tcp_recvmsg`, `tcp_sendmsg` to observe raw TCP/HTTP traffic regardless of language. Parses HTTP bytes from kernel socket buffer.

3. **FastCGI protocol parsing (PHP-FPM specific)**: Added post-1.0 — parses the FastCGI binary protocol over TCP or Unix sockets. Extracts `REQUEST_METHOD`, `SCRIPT_NAME`, `REQUEST_URI`, `CONTENT_TYPE`, `CONTENT_LENGTH`.

### OBI PHP Coverage by Pattern

| Pattern | OBI Mechanism | What OBI Sees | What OBI Cannot See |
|---|---|---|---|
| **PHP-FPM + Nginx** | FastCGI parser | Request method, URI, script name, status, latency (RED metrics) | Internal PHP logic, DB queries, cache calls |
| **Apache mod_php** | Kernel TCP kprobes | HTTP request/response on Apache's socket | Cannot distinguish PHP from Apache C code |
| **Swoole** | Kernel TCP kprobes | HTTP on Swoole's TCP socket | PHP internals, no Swoole-specific probe |
| **FrankenPHP** | Go `net/http` uprobes | Go/Caddy HTTP layer with Go-level precision | PHP execution inside Go process |
| **RoadRunner** | Go `net/http` uprobes | Go HTTP on `rr`'s port | PHP workers use proprietary IPC, not HTTP |
| **PHP CLI workers** | None | — | No network socket to observe |
| **ReactPHP/AMPHP** | Kernel TCP kprobes | HTTP on their TCP socket | PHP internals |

### Key Insight

OBI's PHP-FPM coverage is purpose-built (FastCGI parser). For all other PHP patterns, OBI falls back to generic HTTP tracing or has no coverage at all. OBI is a **network/protocol observer**, not a PHP runtime observer — it cannot see inside PHP code execution.

---

## OTel PHP Extension Details

### How It Works

The `opentelemetry` PECL extension uses PHP 8's `zend_observer` API to register pre/post hook functions on any PHP function/method at the engine level. This is SAPI-agnostic — fires on every PHP function call regardless of how the PHP process was started.

**Requirements**: PHP 8.0+ (8.2+ recommended), Composer autoloading, OTel PHP SDK.

### Compatibility by Runtime

| Runtime | Status | Notes |
|---|---|---|
| **PHP-FPM** | Full support — primary documented use case | Best documented path |
| **Apache mod_php** | Supported, no known blockers | Prefork MPM required for thread safety |
| **PHP CLI** | Full support | Context leak risk in daemon-mode workers |
| **Swoole** | Works with `open-telemetry/context-swoole` package | Auto-instrumentation has reliability issues with Octane |
| **Open Swoole** | Same as Swoole | Community fork, same patterns |
| **ReactPHP** | Full support | Single process, no context isolation issues |
| **AMPHP** | Full support | Uses PHP fibers |
| **Workerman** | Full support | — |
| **FrankenPHP (worker mode)** | **Broken** as of May 2025 | Open issues: env vars ignored, Go/PHP protocol conflict |
| **FrankenPHP (non-worker)** | Likely works | Standard PHP process per request |
| **RoadRunner (PHP workers)** | Works | Idiomatic path: Go OTEL plugin + PHP SDK in workers |
| **PHP built-in server** | Full support | — |

### Key Issues

- **FrankenPHP**: Multiple open unresolved issues (`opentelemetry-php#1611`, `frankenphp#1715`, `frankenphp#1608`). Env vars ignored in worker mode, protocol conflict between Caddy (gRPC) and PHP extension (HTTP/Protobuf). A production case study found OTel auto-instrumentation consumed 44% of system capacity.

- **Swoole context isolation**: Without `context-swoole` package, telemetry context from one coroutine bleeds into another. Even with the package, auto-instrumentation has reliability gaps when combined with Laravel Octane.

- **Long-running CLI workers**: `artisan queue:work` in daemon mode processes many jobs in one PHP process. Context leaks between jobs if not explicitly cleaned up. Best practice: create a fresh root span per job, avoid singleton tracers.

---

## References

### OBI / Beyla
- [OBI Official Documentation](https://opentelemetry.io/docs/zero-code/obi/)
- [OBI First Release Announcement](https://opentelemetry.io/blog/2025/obi-announcing-first-release/)
- [Beyla GitHub Repository](https://github.com/grafana/beyla)
- [Beyla PHP-FPM Support Issue #1395](https://github.com/grafana/beyla/issues/1395)
- [Grafana Beyla 2.0 Blog Post](https://grafana.com/blog/grafana-beyla-2-0-distributed-traces-scalable-kubernetes-deployments-and-more/)

### OTel PHP Extension
- [PHP Zero-Code Instrumentation](https://opentelemetry.io/docs/zero-code/php/)
- [PHP Language Docs](https://opentelemetry.io/docs/languages/php/)
- [PHP Instrumentation Extension GitHub](https://github.com/open-telemetry/opentelemetry-php-instrumentation)
- [Swoole Context Package](https://github.com/open-telemetry/opentelemetry-php-contrib/blob/main/src/Context/Swoole/README.md)
- [FrankenPHP OTel Issue #1611](https://github.com/open-telemetry/opentelemetry-php/issues/1611)

### PHP Runtimes
- [Comparing PHP Application Servers 2025](https://www.deployhq.com/blog/comparing-php-application-servers-in-2025-performance-scalability-and-modern-options)
- [Alternatives to PHP-FPM](https://icinga.com/blog/alternatives-to-php-fpm/)
- [FrankenPHP Documentation](https://frankenphp.dev/docs/)
- [RoadRunner Documentation](https://docs.roadrunner.dev/)
- [Laravel Octane Documentation](https://laravel.com/docs/13.x/octane)
- [LiteSpeed LSPHP Modes](https://docs.litespeedtech.com/lsws/extapp/php/configuration/modes/)
- [Odigos PHP Inspector](https://github.com/odigos-io/odigos/tree/main/procdiscovery/pkg/inspectors/php/php.go)
