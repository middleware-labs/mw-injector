# PHP Deployment Patterns — Process Detection Reference

Exhaustive reference of every way PHP applications run in production on Linux,
with exact process names visible in `/proc`, detection feasibility, and prevalence.

## How to Read This Reference

- **`/proc/<pid>/exe` basename**: the symlink target's filename — `basename $(readlink /proc/<pid>/exe)`
- **`/proc/<pid>/cmdline`**: the null-delimited argv array (shown with spaces for readability)
- **Embedded**: no separate PHP process exists — PHP runs inside another server's address space

---

## 1. Traditional Deployment Patterns

### 1.1 mod_php (Apache + libphp.so)

| Field | Value |
|---|---|
| /proc/exe basename | `apache2` (Debian/Ubuntu) or `httpd` (RHEL/CentOS/Rocky/Alma) |
| /proc/cmdline | `/usr/sbin/apache2 -k start` or `/usr/sbin/httpd -DFOREGROUND` |
| Separate PHP process? | **NO** — libphp.so is dlopen'd into every Apache worker process |
| Detection signal | `cat /proc/<pid>/maps | grep libphp` — if libphp.so appears in an apache2/httpd process, that's mod_php |
| Prevalence 2025/2026 | Declining but still common on legacy shared hosting and Apache prefork MPM setups |

### 1.2 PHP-FPM Master Process

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm8.3` (Debian/Ubuntu, version-suffixed) or `php-fpm` (RHEL/CentOS) |
| /proc/cmdline (Debian) | `php-fpm8.3 --nodaemonize` or `/usr/sbin/php-fpm8.3 --nodaemonize` |
| /proc/cmdline (RHEL) | `php-fpm --nodaemonize` |
| Process title (ps) | `php-fpm: master process (/etc/php/8.3/fpm/php-fpm.conf)` |
| Separate PHP process? | YES |
| Full exe path (Debian) | `/usr/sbin/php-fpm8.1`, `/usr/sbin/php-fpm8.2`, `/usr/sbin/php-fpm8.3` |
| Full exe path (RHEL) | `/usr/sbin/php-fpm` |
| Full exe path (Remi) | `/opt/remi/php83/root/usr/sbin/php-fpm` |
| Prevalence | **Dominant pattern** — used with Nginx, Apache event/worker MPM via proxy_fcgi, Caddy |

### 1.3 PHP-FPM Worker Processes

| Field | Value |
|---|---|
| /proc/exe basename | Same as master: `php-fpm8.3` or `php-fpm` |
| Process title (ps) | `php-fpm: pool www` or `php-fpm: pool <pool_name>` |
| /proc/cmdline | Same binary, no extra args — workers are forked from master |
| Separate PHP process? | YES — each worker is a separate process forked from master |
| Note | Cannot distinguish master from worker via exe alone — use cmdline content ("master process" vs "pool") |

### 1.4 PHP-CGI (spawn-fcgi / standalone)

| Field | Value |
|---|---|
| /proc/exe basename | `php-cgi`, `php-cgi8.3` |
| /proc/cmdline | `/usr/bin/php-cgi` or `php-cgi -b 127.0.0.1:9000` |
| Separate PHP process? | YES |
| Prevalence | Rare in new deployments — legacy shared hosting |

---

## 2. Hosting Panel Managed

### 2.1 cPanel + EasyApache 4 (PHP-FPM handler)

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm` |
| Process title (ps) | `php-fpm: master process (/opt/cpanel/ea-php83/root/etc/php-fpm.conf)` |
| Worker title | `php-fpm: pool <domain>` |
| Full exe path | `/opt/cpanel/ea-php74/root/usr/sbin/php-fpm`, `/opt/cpanel/ea-php81/root/usr/sbin/php-fpm`, `/opt/cpanel/ea-php83/root/usr/sbin/php-fpm` |
| CLI binary | `/opt/cpanel/ea-php83/root/usr/bin/php` |
| Prevalence | Very common — EasyApache 4 defaults to PHP-FPM |

### 2.2 cPanel + EasyApache 4 (suPHP handler, legacy)

| Field | Value |
|---|---|
| /proc/exe basename | `suphp` then `php` |
| suphp binary | `/usr/sbin/suphp` (setuid root, called by mod_suphp Apache module) |
| PHP binary spawned | `/opt/cpanel/ea-php74/root/usr/bin/php` |
| Separate PHP process? | YES — suphp forks a short-lived PHP process per request |
| Prevalence | Legacy/rare — suPHP last released 2013, deprecated in cPanel |

### 2.3 cPanel + EasyApache 4 (FastCGI / mod_fcgid)

| Field | Value |
|---|---|
| /proc/exe basename | `php-cgi` |
| Full exe path | `/opt/cpanel/ea-php83/root/usr/bin/php-cgi` |
| Prevalence | Less common — used when FPM is not enabled |

### 2.4 Plesk PHP-FPM Handler

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm` |
| Full exe path | `/opt/plesk/php/8.1/sbin/php-fpm`, `/opt/plesk/php/8.2/sbin/php-fpm`, `/opt/plesk/php/8.3/sbin/php-fpm` |
| CLI binary | `/opt/plesk/php/8.3/bin/php` |
| Prevalence | Standard for Plesk on Linux |

### 2.5 DirectAdmin + CustomBuild 2.0

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm` |
| Full exe path | `/usr/local/php83/sbin/php-fpm`, `/usr/local/php81/sbin/php-fpm` |
| CLI binary | `/usr/local/php83/bin/php` |
| Prevalence | Common for DirectAdmin setups |

### 2.6 CloudLinux PHP Selector (alt-php) + mod_lsapi

| Field | Value |
|---|---|
| Handler binary | `lsphp` |
| Full exe path | `/opt/alt/php81/usr/bin/lsphp`, `/opt/alt/php82/usr/bin/lsphp`, `/opt/alt/php83/usr/bin/lsphp` |
| Separate PHP process? | YES — lsphp workers managed by mod_lsapi |
| mod_lsapi role | Apache module communicating with lsphp via LSAPI protocol (not FastCGI) |
| Prevalence | Very common on shared hosting using CloudLinux |

### 2.7 CloudLinux alt-php + PHP-FPM

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm` |
| Full exe path | `/opt/alt/php83/usr/sbin/php-fpm` |
| Prevalence | Used when CloudLinux is configured with FPM instead of LSAPI |

---

## 3. LiteSpeed Ecosystem

### 3.1 LiteSpeed Enterprise LSAPI

| Field | Value |
|---|---|
| /proc/exe basename | `lsphp` |
| Full exe path | `/usr/local/lsws/lsphp81/bin/lsphp`, `/usr/local/lsws/lsphp83/bin/lsphp` |
| /proc/cmdline | `lsphp` (minimal args — socket/config via env and inherited FDs) |
| Architecture | ProcessGroup mode: one parent lsphp spawns children; `litespeed` master spawns lsphp parents |
| Prevalence | Very common on LiteSpeed Enterprise hosts (~10% web server market) |

### 3.2 OpenLiteSpeed

| Field | Value |
|---|---|
| /proc/exe basename | `lsphp` |
| Full exe path | `/usr/local/lsws/lsphp74/bin/lsphp`, `/usr/local/lsws/lsphp83/bin/lsphp` |
| Web server process | `/usr/local/lsws/bin/openlitespeed` or `litespeed` |
| Prevalence | Common in self-managed VPS, CyberPanel stacks |

### 3.3 CloudLinux + LiteSpeed + alt-php

| Field | Value |
|---|---|
| /proc/exe basename | `lsphp` |
| Full exe path | `/opt/alt/php83/usr/bin/lsphp` (CloudLinux variant) |
| Note | Different from `/usr/local/lsws/` LiteSpeed native builds — same binary name, different path prefix |

### 3.4 cPanel + LiteSpeed (ea-php lsphp)

| Field | Value |
|---|---|
| /proc/exe basename | `lsphp` |
| Full exe path | `/opt/cpanel/ea-php83/root/usr/bin/lsphp` |

---

## 4. Long-Running Application Servers

### 4.1 Laravel Octane + Swoole

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/app/artisan octane:start --server=swoole --workers=4 --port=8000` |
| Process title (ps, workers) | `octane_worker_0`, `octane_worker_1`, etc. |
| Prevalence | Growing, especially for high-traffic Laravel APIs |

### 4.2 Laravel Octane + RoadRunner

| Field | Value |
|---|---|
| /proc/exe basename (server) | `rr` (RoadRunner Go binary) |
| /proc/cmdline (server) | `rr serve` or `rr serve -c .rr.yaml` |
| /proc/exe basename (workers) | `php` |
| /proc/cmdline (workers) | `php /var/www/app/artisan octane:start --server=roadrunner` or `php /path/to/worker.php` |
| Prevalence | Moderate and growing |

### 4.3 Laravel Octane + FrankenPHP

| Field | Value |
|---|---|
| /proc/exe basename | `frankenphp` |
| /proc/cmdline | `frankenphp run --config /path/to/Caddyfile` |
| Architecture | Single process, multi-threaded — PHP runs as threads inside frankenphp, no separate PHP workers |
| Full exe path | `/usr/local/bin/frankenphp` |
| Prevalence | Rapidly growing — newest option, favored for Docker/single-binary deployments |

### 4.4 Swoole Standalone (not via Laravel)

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/server.php` |
| Process title (ps, master) | `swoole-master` |
| Process title (ps, manager) | `swoole-manager` |
| Process title (ps, workers) | `swoole-worker #0`, `swoole-worker #1` |
| Prevalence | Moderate — used by HyperF, EasySwoole, Swoft frameworks |

### 4.5 Open Swoole Standalone

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| Process title | `open-swoole-master`, `open-swoole-manager`, `open-swoole-worker #N` |
| Prevalence | Niche — community fork of Swoole |

### 4.6 ReactPHP Standalone

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/server.php` |
| Process title | `php` — ReactPHP does not rename the process |
| Architecture | Single-threaded, single-process event loop |
| Prevalence | Niche but stable — WebSocket servers, microservices |

### 4.7 AMPHP Standalone

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/server.php` |
| Process title | `php` — no rename |
| Architecture | Single process using PHP fibers/coroutines |
| Prevalence | Niche |

### 4.8 Workerman / webman

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/start.php start -d` |
| Process title (master) | `WorkerMan: master process  start.php` |
| Process title (workers) | `WorkerMan: worker process  [WorkerName] [socket]` |
| Prevalence | Significant in Asian markets — webman framework has substantial adoption |

### 4.9 PHP Built-in Development Server

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php -S 0.0.0.0:8000 -t /var/www/public` |
| Prevalence | Development only — occasionally in CI environments |

---

## 5. Containerized Patterns

### 5.1 Docker Official `php:fpm` Image

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm` |
| /proc/cmdline | `php-fpm --nodaemonize` |
| Full exe path inside container | `/usr/local/sbin/php-fpm` (compiled from source) |
| Process title (master) | `php-fpm: master process (/usr/local/etc/php-fpm.conf)` |
| Process title (workers) | `php-fpm: pool www` or `php-fpm: pool docker` |
| Prevalence | Very common — de facto standard for containerized PHP |

### 5.2 Docker Official `php:apache` Image (mod_php in container)

| Field | Value |
|---|---|
| /proc/exe basename | `apache2` |
| /proc/cmdline | `apache2 -DFOREGROUND` |
| Separate PHP process? | **NO** — libphp embedded in apache2 |
| Prevalence | Common for quick deployments and WordPress Docker setups |

### 5.3 Docker Official `php:cli` Image

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /app/some-script.php` or `php artisan queue:work` |
| Prevalence | Common for queue workers and cron containers |

### 5.4 Docker Compose nginx + php-fpm

| Field | Value |
|---|---|
| nginx container | exe: `nginx`, cmdline: `nginx -g daemon off;` |
| php-fpm container | exe: `php-fpm`, cmdline: `php-fpm --nodaemonize` |
| Host /proc visibility | All container processes visible in host's /proc with real exe names |
| Prevalence | Extremely common — canonical Docker Compose PHP setup |

### 5.5 Kubernetes PHP-FPM Sidecar

| Field | Value |
|---|---|
| App container | exe: `php-fpm`, process title: `php-fpm: master process (...)` |
| Nginx sidecar | exe: `nginx` |
| /proc visibility | Pod processes visible on node's /proc |

### 5.6 FrankenPHP Docker Image

| Field | Value |
|---|---|
| /proc/exe basename | `frankenphp` |
| /proc/cmdline | `frankenphp run` or `frankenphp php-server` |
| Full exe path | `/usr/local/bin/frankenphp` |

---

## 6. Process Manager Supervised PHP

### 6.1 Supervisord Managing PHP Workers

| Field | Value |
|---|---|
| PHP worker processes | exe: `php`, cmdline: `php /var/www/app/artisan queue:work --queue=default --tries=3` |
| Prevalence | Very common for production queue workers |

### 6.2 systemd Managing php-fpm

| Field | Value |
|---|---|
| Service name | `php8.3-fpm.service` (Debian) or `php-fpm.service` (RHEL) |
| /proc/exe basename | `php-fpm8.3` or `php-fpm` |
| /proc/cmdline | `/usr/sbin/php-fpm8.3 --nodaemonize --fpm-config /etc/php/8.3/fpm/php-fpm.conf` |
| Prevalence | Dominant for bare-metal/VPS PHP-FPM |

### 6.3 s6-overlay in Docker

| Field | Value |
|---|---|
| PHP-FPM process | exe: `php-fpm`, cmdline: `php-fpm --nodaemonize` |
| Note | s6 processes (`s6-svscan`, `s6-supervise`) are supervisors, not PHP |
| Prevalence | Common in production Docker images (serversideup/php, Bitnami) |

---

## 7. Framework-Specific CLI Workers

### 7.1 Laravel artisan queue:work

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/app/artisan queue:work --queue=default --tries=3 --timeout=90` |
| Prevalence | Ubiquitous in Laravel production |

### 7.2 Laravel artisan schedule:run / schedule:work

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/app/artisan schedule:run` (cron-invoked, short-lived) or `php /var/www/app/artisan schedule:work` (long-running loop) |

### 7.3 Symfony Console Workers

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/app/bin/console messenger:consume async --time-limit=3600` |

### 7.4 Drupal drush cron

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `/usr/local/bin/drush --root=/var/www/drupal cron` or `php /usr/local/bin/drush cron` |

### 7.5 Magento cron / indexer

| Field | Value |
|---|---|
| /proc/exe basename | `php` |
| /proc/cmdline | `php /var/www/magento2/bin/magento cron:run --group=default` or `php /var/www/magento2/bin/magento indexer:reindex` |

### 7.6 WordPress wp-cron.php

| Field | Value |
|---|---|
| Mechanism | Triggered by HTTP requests via curl/wget from system cron — not a standalone process |
| /proc visibility | Appears as apache2/httpd (mod_php) or php-fpm worker — no dedicated process name |
| WP-CLI alternative | `php /var/www/wp/wp-cli.phar cron event run --due-now` |

---

## 8. Exotic / Emerging Patterns

### 8.1 Static PHP Binary (static-php-cli, phc)

| Field | Value |
|---|---|
| /proc/exe basename | User-defined binary name (e.g., `myapp`, `api-server`) |
| Detection | Requires ELF inspection or `/proc/<pid>/maps` for PHP symbols |
| Prevalence | Rare in server production — growing for CLI tools distributed as single binaries |

### 8.2 Bref (PHP on AWS Lambda)

| Field | Value |
|---|---|
| /proc/exe basename | `php-fpm` (FPM runtime) or `php` (function runtime) |
| Note | Runs inside Lambda micro-VM — not detectable via host /proc scanning |
| Prevalence | Growing for serverless PHP |

### 8.3 PeachPie (PHP on .NET CLR)

| Field | Value |
|---|---|
| /proc/exe basename | `dotnet` or compiled assembly name |
| Note | No PHP executable involved — PHP compiled to .NET IL |
| Prevalence | Experimental/niche |

---

## Summary: Executable Name → Runtime Mapping

| Executable Basename Pattern | Runtime | Notes |
|---|---|---|
| `php` | PHP CLI | Bare interpreter — scripts, queue workers, Swoole, ReactPHP, etc. |
| `php[0-9.]+` (e.g. `php8.2`, `php82`) | PHP CLI (versioned) | Distro-specific versioned binary |
| `php-fpm` | PHP-FPM | RHEL/Docker single binary |
| `php-fpm[0-9.]+` (e.g. `php-fpm8.2`) | PHP-FPM (versioned) | Debian/Ubuntu versioned binary |
| `php-cgi` | PHP-CGI | Legacy FastCGI |
| `php-cgi[0-9.]+` | PHP-CGI (versioned) | Legacy FastCGI versioned |
| `lsphp` | LiteSpeed LSAPI | LiteSpeed/OpenLiteSpeed/CloudLinux |
| `lsphp[0-9.]+` (e.g. `lsphp82`) | LiteSpeed LSAPI (versioned) | Version-specific LSAPI binary |
| `frankenphp` | FrankenPHP | Go/Caddy + embedded PHP (threads, no PHP child processes) |
| `rr` | RoadRunner | Go binary — PHP workers are separate `php` processes |
| `apache2` / `httpd` | mod_php (embedded) | Check `/proc/<pid>/maps` for `libphp` to confirm |
